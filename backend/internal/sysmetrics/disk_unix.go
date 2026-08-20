//go:build linux || darwin

package sysmetrics

import "golang.org/x/sys/unix"

func diskUsage(path string) (used, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	// Prefer Bavail (free for non-root) for usable capacity.
	bsize := uint64(st.Bsize)
	total = st.Blocks * bsize
	free := st.Bavail * bsize
	if total < free {
		return 0, total, nil
	}
	used = total - free
	return used, total, nil
}
