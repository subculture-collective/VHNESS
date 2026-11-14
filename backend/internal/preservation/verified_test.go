package preservation

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
)

// nextTick waits long enough for the kernel's coarse file timestamps to move.
func nextTick() { time.Sleep(50 * time.Millisecond) }

func verifiedFixture(t *testing.T) (*VerifiedFiles, string, []byte, func(time.Duration)) {
	t.Helper()
	root := t.TempDir()
	content := []byte("immutable derived audio fixture")
	if err := os.WriteFile(filepath.Join(root, "a.wav"), content, 0600); err != nil {
		t.Fatal(err)
	}
	// The clock starts past the racy window so fixture files count as quiet.
	var mu sync.Mutex
	offset := time.Minute
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return time.Now().Add(offset) }
	advance := func(d time.Duration) { mu.Lock(); offset += d; mu.Unlock() }
	return &VerifiedFiles{now: now}, root, content, advance
}

func readAll(t *testing.T, v *VerifiedFiles, root string, content []byte) error {
	t.Helper()
	f, err := v.Verify(root, "a.wav", domain.Digest(content), int64(len(content)))
	if err != nil {
		return err
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatal("verified file not returned at offset zero", err, string(got))
	}
	return nil
}

func TestVerifiedFilesHashOnceWhileUnchanged(t *testing.T) {
	v, root, content, advance := verifiedFixture(t)
	for i := 0; i < 5; i++ {
		if err := readAll(t, v, root, content); err != nil {
			t.Fatal(err)
		}
	}
	if v.Hashed() != 1 {
		t.Fatal("unchanged file reread", v.Hashed())
	}
	advance(ReverifyAfter)
	if err := readAll(t, v, root, content); err != nil || v.Hashed() != 2 {
		t.Fatal("expired verification not repeated", err, v.Hashed())
	}
	if err := readAll(t, v, root, content); err != nil || v.Hashed() != 2 {
		t.Fatal("renewed verification not reused", err, v.Hashed())
	}
}

func TestVerifiedFilesRefuseChangedBytes(t *testing.T) {
	v, root, content, _ := verifiedFixture(t)
	path := filepath.Join(root, "a.wav")
	if err := readAll(t, v, root, content); err != nil {
		t.Fatal(err)
	}
	original, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	nextTick()
	// Same size, different bytes, with the modification time put back:
	// only the change time and a fresh hash can catch this.
	changed := bytes.ToUpper(content)
	if err = os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(path, original.ModTime(), original.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err = readAll(t, v, root, content); err == nil {
		t.Fatal("changed bytes served from earlier verification")
	}
	if err = readAll(t, v, root, content); err == nil || v.Hashed() != 3 {
		t.Fatal("failed verification was trusted", err, v.Hashed())
	}
	nextTick()
	if err = os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	if err = readAll(t, v, root, content); err != nil {
		t.Fatal("restored bytes refused", err)
	}
	// A replacement file with the same bytes is a new inode and is hashed again.
	if err = os.WriteFile(path+".new", content, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(path+".new", path); err != nil {
		t.Fatal(err)
	}
	before := v.Hashed()
	if err = readAll(t, v, root, content); err != nil || v.Hashed() != before+1 {
		t.Fatal("replaced file not rehashed", err, v.Hashed(), before)
	}
	if _, err = v.Verify(root, "a.wav", domain.Digest(content), int64(len(content))+1); err == nil {
		t.Fatal("size mismatch accepted")
	}
	if _, err = v.Verify(root, "a.wav", domain.Digest([]byte("other")), int64(len(content))); err == nil {
		t.Fatal("different expected hash reused the earlier verification")
	}
}

func TestVerifiedFilesRacyAndNil(t *testing.T) {
	root := t.TempDir()
	content := []byte("just written")
	if err := os.WriteFile(filepath.Join(root, "a.wav"), content, 0600); err != nil {
		t.Fatal(err)
	}
	v := &VerifiedFiles{}
	for i := 0; i < 3; i++ {
		if err := readAll(t, v, root, content); err != nil {
			t.Fatal(err)
		}
	}
	if v.Hashed() != 3 {
		t.Fatal("file changed within the racy window was trusted", v.Hashed())
	}
	var none *VerifiedFiles
	if err := readAll(t, none, root, content); err != nil || none.Hashed() != 0 {
		t.Fatal("nil verifier", err)
	}
}

func TestVerifiedFilesConcurrentColdReadHashesOnce(t *testing.T) {
	v, root, content, _ := verifiedFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := v.Verify(root, "a.wav", domain.Digest(content), int64(len(content)))
			if err == nil {
				f.Close()
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if v.Hashed() != 1 {
		t.Fatal("concurrent cold reads each hashed the file", v.Hashed())
	}
}
