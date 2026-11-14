package preservation

import (
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// ReverifyAfter bounds how long a verified file is trusted without rereading
// it, so corruption that leaves file metadata unchanged is still found.
const ReverifyAfter = 24 * time.Hour

// racyWindow is how recently a file may have changed and still be trusted
// from its metadata. Kernel timestamps are coarse, so a second write within
// the same tick can leave them unchanged; such a file is hashed every time
// until it has been quiet for this long.
const racyWindow = time.Second

// VerifiedFiles serves repeated reads of large immutable files without
// hashing them on every request. A file is hashed once; later calls trust it
// while its identity (device, inode, size, modification and change times) is
// unchanged and the check is younger than ReverifyAfter. Any change in that
// identity makes the next call hash the file again, so changed bytes are
// still refused. The zero value is ready to use; a nil receiver hashes on
// every call, exactly like Verify.
type VerifiedFiles struct {
	mu      sync.Mutex
	entries map[verifiedKey]*verifiedEntry
	hashed  atomic.Int64
	now     func() time.Time
}

var errChangedDuringCheck = errors.New("local original changed during integrity check")

type verifiedKey struct {
	root, path, hash string
	bytes            int64
}

type verifiedEntry struct {
	mu       sync.Mutex
	identity fileIdentity
	at       time.Time
	ok       bool
}

// Hashed reports how many full-file hashes this cache has performed.
func (v *VerifiedFiles) Hashed() int64 {
	if v == nil {
		return 0
	}
	return v.hashed.Load()
}

// Verify behaves like the package-level Verify but skips rereading a file
// whose identity matches its last successful check.
func (v *VerifiedFiles) Verify(root, path, hash string, bytes int64) (*os.File, error) {
	if v == nil {
		return Verify(root, path, hash, bytes)
	}
	f, info, err := openLocal(root, path, bytes)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if v.now != nil {
		now = v.now()
	}
	identity, known := identityOf(info)
	entry := v.entry(verifiedKey{root, path, hash, bytes})
	// Holding the entry lock while hashing makes concurrent requests for a
	// cold file wait for one read instead of each reading it.
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if known && entry.ok && entry.identity == identity && now.Sub(entry.at) < ReverifyAfter {
		return f, nil
	}
	entry.ok = false
	v.hashed.Add(1)
	if err = checkHash(f, hash); err != nil {
		f.Close()
		return nil, err
	}
	after, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if afterIdentity, _ := identityOf(after); afterIdentity != identity {
		f.Close()
		return nil, errChangedDuringCheck
	}
	if known && identity.quietSince(now, racyWindow) {
		entry.identity, entry.at, entry.ok = identity, now, true
	}
	return f, nil
}

func (v *VerifiedFiles) entry(key verifiedKey) *verifiedEntry {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.entries == nil {
		v.entries = map[verifiedKey]*verifiedEntry{}
	}
	entry := v.entries[key]
	if entry == nil {
		entry = &verifiedEntry{}
		v.entries[key] = entry
	}
	return entry
}
