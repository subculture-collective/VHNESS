// Package preservation owns explicit, bounded original acquisition. Metadata workers never use it.
package preservation

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"git.subcult.tv/subculture-collective/vhness/internal/source"
)

type Manifest struct {
	Collection string `json:"collection"`
	ID         string `json:"id"`
	Snapshot   string `json:"snapshot_sha256"`
	File       string `json:"file"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
	Notes      string `json:"notes"`
}
type Receipt struct {
	Manifest   Manifest  `json:"manifest"`
	Path       string    `json:"path"`
	SHA256     string    `json:"sha256"`
	MD5        string    `json:"md5"`
	VerifiedAt time.Time `json:"verified_at"`
}
type partial struct {
	Manifest Manifest `json:"manifest"`
	ETag     string   `json:"etag"`
}
type Acquirer struct {
	Root    string
	HTTP    *http.Client
	Base    string
	Free    func(string) (int64, error)
	Fixture bool
}

func New(root string) *Acquirer {
	return &Acquirer{Root: root, Base: "https://archive.org", Free: FreeBytes, HTTP: &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) == 0 || len(via) > 5 || !source.Allowed(r.URL) {
			return fmt.Errorf("media redirect rejected")
		}
		identity := strings.TrimPrefix(via[0].URL.Path, "/download/")
		if r.URL.Path != via[0].URL.Path && !strings.HasSuffix(r.URL.Path, "/items/"+identity) {
			return fmt.Errorf("media redirect rejected")
		}
		return nil
	}}}
}
func FreeBytes(root string) (int64, error) {
	var stat syscall.Statfs_t
	err := syscall.Statfs(root, &stat)
	return int64(stat.Bavail) * stat.Bsize, err
}
func Usage(root string) (int64, error) {
	var size int64
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks forbidden in preservation root")
		}
		if !d.IsDir() {
			i, err := d.Info()
			if err != nil {
				return err
			}
			if !i.Mode().IsRegular() {
				return fmt.Errorf("nonregular preservation asset")
			}
			size += i.Size()
		}
		return nil
	})
	return size, err
}
func (m Manifest) Validate(c domain.Collection, e domain.Entry) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Policy != domain.Local || e.Policy != domain.Local || c.ID == domain.DemocracyNow || e.Episode.DemocracySource() {
		return fmt.Errorf("streaming-policy acquisition forbidden")
	}
	f, ok := e.SelectedOriginal()
	if !ok || !e.Decision.Acquisition || m.Collection != c.ID || e.Episode.CollectionID != c.ID || m.ID != e.Episode.ID || m.File != f.Name || m.Bytes != f.Bytes || m.Snapshot != e.SourceHash || strings.TrimSpace(m.Notes) == "" {
		return fmt.Errorf("manifest does not match reviewed source snapshot")
	}
	if c.Budget < 4096 || m.Bytes > c.Budget-4096 {
		return fmt.Errorf("manifest exceeds budget with receipt reserve")
	}
	if m.SHA256 != "" {
		hash, err := hex.DecodeString(m.SHA256)
		if err != nil || len(hash) != 32 || hex.EncodeToString(hash) != m.SHA256 {
			return fmt.Errorf("invalid expected SHA-256")
		}
	} else {
		md, mdErr := hex.DecodeString(f.MD5)
		sha, shaErr := hex.DecodeString(f.SHA1)
		if (mdErr != nil || len(md) != 16) && (shaErr != nil || len(sha) != 20) {
			return fmt.Errorf("manifest requires SHA-256 or pinned source checksum")
		}
	}
	return nil
}
func HashAll(r io.Reader) (string, string, string, int64, error) {
	s := sha256.New()
	m := md5.New()
	one := sha1.New()
	n, err := io.Copy(io.MultiWriter(s, m, one), r)
	return hex.EncodeToString(s.Sum(nil)), hex.EncodeToString(m.Sum(nil)), hex.EncodeToString(one.Sum(nil)), n, err
}
func Hash(r io.Reader) (string, string, int64, error) {
	s, m, _, n, err := HashAll(r)
	return s, m, n, err
}
func Verify(root, path, hash string, bytes int64) (*os.File, error) {
	f, _, err := openLocal(root, path, bytes)
	if err != nil {
		return nil, err
	}
	if err = checkHash(f, hash); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// openLocal opens a regular file inside root and checks its expected size.
func openLocal(root, path string, bytes int64) (*os.File, os.FileInfo, error) {
	jail, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, err
	}
	defer jail.Close()
	f, err := jail.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != bytes {
		f.Close()
		return nil, nil, fmt.Errorf("local original size/type mismatch")
	}
	return f, info, nil
}

// checkHash reads the whole file and leaves it at offset zero when it matches.
func checkHash(f *os.File, hash string) error {
	actual, _, _, err := Hash(f)
	if err != nil || actual != hash {
		return fmt.Errorf("local original integrity mismatch")
	}
	_, err = f.Seek(0, io.SeekStart)
	return err
}

func (a *Acquirer) Acquire(ctx context.Context, c domain.Collection, e domain.Entry, m Manifest) (Receipt, error) {
	var receipt Receipt
	if a.Fixture && (!e.Episode.Synthetic || c.ID != "vhness-local-fixture" || m.Bytes > 10<<20) {
		return receipt, fmt.Errorf("fixture acquisition requires the bounded generated collection")
	}
	if err := m.Validate(c, e); err != nil {
		return receipt, err
	}
	if !filepath.IsAbs(a.Root) {
		return receipt, fmt.Errorf("absolute preservation root required")
	}
	if err := os.MkdirAll(a.Root, 0700); err != nil {
		return receipt, err
	}
	lock, err := os.OpenFile(filepath.Join(a.Root, ".acquisition.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return receipt, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return receipt, fmt.Errorf("another acquisition owns this root")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if _, err = Usage(a.Root); err != nil {
		return receipt, err
	}
	if err = os.MkdirAll(filepath.Join(a.Root, "originals"), 0700); err != nil {
		return receipt, err
	}
	target := filepath.Join("originals", m.SHA256+strings.ToLower(filepath.Ext(m.File)))
	expectedHash := m.SHA256
	if expectedHash == "" && e.LocalSnapshot == m.Snapshot && e.LocalFile == m.File && e.LocalSHA256 != "" {
		expectedHash = e.LocalSHA256
		target = filepath.Join("originals", expectedHash+strings.ToLower(filepath.Ext(m.File)))
	}
	if expectedHash != "" {
		if _, err = os.Stat(filepath.Join(a.Root, target)); err == nil {
			f, err := Verify(a.Root, target, expectedHash, m.Bytes)
			if err != nil {
				return receipt, err
			}
			defer f.Close()
			actualHash, md, one, _, err := HashAll(f)
			if err != nil {
				return receipt, err
			}
			selected, _ := e.SelectedOriginal()
			if (selected.MD5 != "" && !strings.EqualFold(selected.MD5, md)) || (selected.SHA1 != "" && !strings.EqualFold(selected.SHA1, one)) {
				return receipt, fmt.Errorf("source MD5 mismatch")
			}
			return Receipt{m, target, actualHash, md, time.Now().UTC()}, nil
		}
	}
	fingerprint := domain.Digest([]byte(m.Collection + "/" + m.ID + "/" + m.File))
	part := filepath.Join(a.Root, fingerprint+".part")
	meta := part + ".json"
	prior := partial{Manifest: m}
	offset := int64(0)
	if info, err := os.Stat(part); err == nil {
		if !info.Mode().IsRegular() {
			return receipt, fmt.Errorf("nonregular partial")
		}
		offset = info.Size()
		b, err := os.ReadFile(meta)
		if err != nil {
			return receipt, err
		}
		if err = json.Unmarshal(b, &prior); err != nil {
			return receipt, err
		}
		if prior.Manifest != m {
			return receipt, fmt.Errorf("changed manifest; retained partial requires operator review")
		}
	}
	if offset > m.Bytes {
		return receipt, fmt.Errorf("partial exceeds manifest")
	}
	check := func(remaining int64) error {
		used, err := Usage(a.Root)
		if err != nil {
			return err
		}
		if used > c.Budget || remaining > c.Budget-used {
			return fmt.Errorf("preservation byte budget exhausted")
		}
		free, err := a.Free(a.Root)
		if err != nil {
			return err
		}
		if free < c.Reserve || remaining > free-c.Reserve {
			return fmt.Errorf("free-space reserve would be violated")
		}
		return nil
	}
	if err = check(m.Bytes - offset + 4096); err != nil {
		return receipt, err
	}
	if offset < m.Bytes {
		sourceID := m.ID
		if e.Episode.SourceItemID != "" {
			sourceID = e.Episode.SourceItemID
		}
		req, err := http.NewRequestWithContext(ctx, "GET", a.Base+"/download/"+url.PathEscape(sourceID)+"/"+url.PathEscape(m.File), nil)
		if err != nil {
			return receipt, err
		}
		req.Header.Set("Accept-Encoding", "identity")
		req.Header.Set("User-Agent", "VHNESS/0.1 (reviewed local preservation)")
		if offset > 0 {
			if prior.ETag == "" || strings.HasPrefix(prior.ETag, "W/") {
				return receipt, fmt.Errorf("resume requires strong source ETag")
			}
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
			req.Header.Set("If-Range", prior.ETag)
		}
		response, err := a.HTTP.Do(req)
		if err != nil {
			return receipt, err
		}
		defer response.Body.Close()
		if response.StatusCode == 429 || response.StatusCode >= 500 {
			return receipt, &source.Backoff{Until: source.RetryDeadline(response.Header.Get("Retry-After"), time.Now()), Status: response.StatusCode}
		}
		expected := m.Bytes - offset
		if response.ContentLength != expected || response.Header.Get("Content-Encoding") != "" {
			return receipt, fmt.Errorf("source length/encoding changed")
		}
		if offset == 0 && response.StatusCode != 200 {
			return receipt, fmt.Errorf("initial source status %d", response.StatusCode)
		}
		if offset > 0 && (response.StatusCode != 206 || response.Header.Get("ETag") != prior.ETag || response.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", offset, m.Bytes-1, m.Bytes)) {
			return receipt, fmt.Errorf("resume identity/range rejected; partial retained")
		}
		prior.ETag = response.Header.Get("ETag")
		b, _ := json.Marshal(prior)
		if err = os.WriteFile(meta, b, 0600); err != nil {
			return receipt, err
		}
		f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return receipt, err
		}
		buffer := make([]byte, 64<<10)
		for offset < m.Bytes {
			if err = ctx.Err(); err != nil {
				f.Sync()
				f.Close()
				return receipt, err
			}
			remaining := m.Bytes - offset
			chunk := int64(len(buffer))
			if chunk > remaining {
				chunk = remaining
			}
			if err = check(chunk); err != nil {
				f.Sync()
				f.Close()
				return receipt, err
			}
			n, readErr := response.Body.Read(buffer[:chunk])
			if n > 0 {
				written, writeErr := f.Write(buffer[:n])
				offset += int64(written)
				if writeErr != nil {
					f.Close()
					return receipt, writeErr
				}
				if written != n {
					f.Close()
					return receipt, io.ErrShortWrite
				}
			}
			if readErr != nil {
				if readErr == io.EOF && offset == m.Bytes {
					break
				}
				f.Sync()
				f.Close()
				return receipt, fmt.Errorf("interrupted transfer: %w", readErr)
			}
		}
		if err = f.Sync(); err != nil {
			f.Close()
			return receipt, err
		}
		if err = f.Close(); err != nil {
			return receipt, err
		}
	}
	f, err := os.Open(part)
	if err != nil {
		return receipt, err
	}
	hash, md, one, bytes, err := HashAll(f)
	f.Close()
	if err != nil {
		return receipt, err
	}
	selected, _ := e.SelectedOriginal()
	if bytes != m.Bytes || (m.SHA256 != "" && hash != m.SHA256) || (selected.MD5 != "" && !strings.EqualFold(md, selected.MD5)) || (selected.SHA1 != "" && !strings.EqualFold(one, selected.SHA1)) {
		return receipt, fmt.Errorf("original checksum mismatch; partial retained")
	}
	target = filepath.Join("originals", hash+strings.ToLower(filepath.Ext(m.File)))
	// Link is exclusive: a verified original is immutable and never overwritten.
	if err = os.Link(part, filepath.Join(a.Root, target)); err != nil {
		if !os.IsExist(err) {
			return receipt, err
		}
		existing, checkErr := Verify(a.Root, target, hash, m.Bytes)
		if checkErr != nil {
			return receipt, checkErr
		}
		existing.Close()
	}
	receipt = Receipt{m, target, hash, md, time.Now().UTC()}
	b, _ := json.MarshalIndent(receipt, "", "  ")
	if err = os.WriteFile(filepath.Join(a.Root, fingerprint+"-"+hash+".receipt.json"), b, 0600); err != nil {
		return receipt, err
	}
	if err = os.Remove(part); err != nil {
		return receipt, err
	}
	if err = os.Remove(meta); err != nil {
		return receipt, err
	}
	directory, err := os.Open(a.Root)
	if err == nil {
		err = directory.Sync()
		directory.Close()
	}
	return receipt, err
}

type fixtureTransport struct{ path string }

func (t fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f, err := os.Open(t.path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	hash, _, _, err := Hash(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	offset := int64(0)
	status := 200
	header := http.Header{"ETag": {`"` + hash + `"`}}
	if requested := r.Header.Get("Range"); requested != "" {
		if _, err = fmt.Sscanf(requested, "bytes=%d-", &offset); err != nil || offset < 0 || offset >= info.Size() {
			f.Close()
			return nil, fmt.Errorf("invalid fixture range")
		}
		if r.Header.Get("If-Range") != header.Get("ETag") {
			f.Close()
			return nil, fmt.Errorf("fixture changed")
		}
		status = 206
		header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, info.Size()-1, info.Size()))
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return &http.Response{StatusCode: status, Header: header, Body: f, ContentLength: info.Size() - offset, Request: r}, nil
}

// GeneratedFixture exercises the same acquisition pipeline with an explicit generated local input.
func GeneratedFixture(root, original string) *Acquirer {
	a := New(root)
	a.Fixture = true
	a.HTTP = &http.Client{Transport: fixtureTransport{original}}
	return a
}
