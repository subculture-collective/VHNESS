package preservation

import (
	"os"
	"syscall"
	"time"
)

type fileIdentity struct {
	dev, ino     uint64
	size         int64
	mtime, ctime syscall.Timespec
}

// identityOf reports false when the platform gives no inode and change time.
func identityOf(info os.FileInfo) (fileIdentity, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileIdentity{}, false
	}
	return fileIdentity{dev: uint64(st.Dev), ino: st.Ino, size: st.Size, mtime: st.Mtim, ctime: st.Ctim}, true
}

// quietSince reports whether the file last changed at least window before now.
func (i fileIdentity) quietSince(now time.Time, window time.Duration) bool {
	latest := time.Unix(i.mtime.Unix())
	if changed := time.Unix(i.ctime.Unix()); changed.After(latest) {
		latest = changed
	}
	return now.Sub(latest) >= window
}
