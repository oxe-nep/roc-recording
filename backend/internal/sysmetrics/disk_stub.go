//go:build !linux && !darwin

package sysmetrics

import "fmt"

func diskUsage(path string) (used, total uint64, err error) {
	return 0, 0, fmt.Errorf("disk usage not supported on this platform")
}
