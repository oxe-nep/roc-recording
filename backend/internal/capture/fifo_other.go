//go:build !linux

package capture

import "fmt"

func mkfifo(path string, mode uint32) error {
	return fmt.Errorf("mkfifo not supported on this platform")
}
