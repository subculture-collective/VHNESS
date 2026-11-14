// Package audio verifies continuous derivatives separately from immutable originals.
package audio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/catalog"
	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"git.subcult.tv/subculture-collective/vhness/internal/preservation"
)

// Verify opens the derived file only after current rights and every input pass.
// With files set, inputs and the derivative are hashed once and trusted while
// their file identity is unchanged; a nil files hashes them on every call.
func Verify(files *preservation.VerifiedFiles, mediaRoot, derivativeRoot string, e catalog.TransportEntry, asset domain.AudioAsset, action domain.Action) (*os.File, error) {
	if !filepath.IsAbs(mediaRoot) || !filepath.IsAbs(derivativeRoot) {
		return nil, fmt.Errorf("separate audio original and derivative roots required")
	}
	mediaPath, err := filepath.EvalSymlinks(mediaRoot)
	if err != nil {
		return nil, err
	}
	derivedPath, err := filepath.EvalSymlinks(derivativeRoot)
	if err != nil {
		return nil, err
	}
	if mediaPath == derivedPath || strings.HasPrefix(derivedPath, mediaPath+string(filepath.Separator)) || strings.HasPrefix(mediaPath, derivedPath+string(filepath.Separator)) {
		return nil, fmt.Errorf("audio roots overlap")
	}
	if err := domain.AuthorizeAudioAsset(e.Collection, e.Representation, e.Raw, e.Review, e.Preservation, asset, action); err != nil {
		return nil, err
	}
	for _, track := range e.Representation.Extension.Audio.Recording.Tracks {
		found := false
		for _, original := range e.Preservation.Manifest {
			if original.File != track.File.Name {
				continue
			}
			if original.Bytes != track.File.Bytes {
				return nil, fmt.Errorf("track byte count differs from preserved original")
			}
			f, err := files.Verify(mediaRoot, original.Path, original.SHA256, original.Bytes)
			if err != nil {
				return nil, err
			}
			f.Close()
			found = true
		}
		if !found {
			return nil, fmt.Errorf("recording input unavailable")
		}
	}
	return files.Verify(derivativeRoot, asset.Path, asset.SHA256, asset.Bytes)
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, fmt.Errorf("audio probe exceeds bound")
	}
	return b.Buffer.Write(p)
}
func Probe(ctx context.Context, f *os.File, expected float64) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "prlimit", "--as=536870912", "--cpu=20", "--", "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", "/proc/self/fd/3")
	cmd.ExtraFiles = []*os.File{f}
	out, stderr := &boundedBuffer{limit: 4096}, &boundedBuffer{limit: 8192}
	cmd.Stdout, cmd.Stderr = out, stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("audio duration probe failed: %w", err)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(out.String()), 64)
	if err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || math.Abs(duration-expected) > 0.15 {
		return fmt.Errorf("derived audio duration differs from reviewed recording")
	}
	return nil
}

func Register(ctx context.Context, db *catalog.DB, mediaRoot, derivativeRoot, item, representation, collection string, asset domain.AudioAsset, notes string) error {
	e, err := db.Transport(ctx, item, representation, collection)
	if err != nil {
		return err
	}
	f, err := Verify(nil, mediaRoot, derivativeRoot, e, asset, domain.PlayAction)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = Probe(ctx, f, asset.Duration); err != nil {
		return err
	}
	return db.RegisterAudioAsset(ctx, item, representation, collection, asset, notes)
}
