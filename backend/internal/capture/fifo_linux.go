package capture

import "golang.org/x/sys/unix"

func mkfifo(path string, mode uint32) error {
	return unix.Mkfifo(path, mode)
}
