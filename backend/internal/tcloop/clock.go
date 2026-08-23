package tcloop

import (
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var reTimecodeLine = regexp.MustCompile(`^\d{1,2}:\d{2}:\d{2}([:.;]\d{2})?$`)

func defaultUDPPort(id int) int {
	return 9300 + id
}

// writeClockFile atomically replaces path so FFmpeg drawtext reload=1 never
// observes a truncated/empty file mid-write (a known crash source).
func writeClockFile(path, text string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".roc-tc-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.WriteString(text); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func startTODClockFile(path string, stop <-chan struct{}) error {
	write := func() error {
		return writeClockFile(path, time.Now().Format("15:04:05"))
	}
	if err := write(); err != nil {
		return fmt.Errorf("tod clock file: %w", err)
	}
	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_ = write()
			}
		}
	}()
	return nil
}

func startUDPClockFile(path string, port int, stop <-chan struct{}) error {
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("udp addr: %w", err)
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return fmt.Errorf("udp listen :%d: %w", port, err)
	}
	if err := writeClockFile(path, "--:--:--"); err != nil {
		_ = conn.Close()
		return fmt.Errorf("udp clock file: %w", err)
	}

	// Unblock ReadFromUDP immediately when the TC process stops / restarts.
	go func() {
		<-stop
		_ = conn.Close()
	}()

	go func() {
		buf := make([]byte, 512)
		last := ""
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				select {
				case <-stop:
					return
				default:
					continue
				}
			}
			tc := normalizeTimecode(string(buf[:n]))
			if tc == "" || tc == last {
				continue
			}
			if err := writeClockFile(path, tc); err != nil {
				log.Printf("[tcloop] udp clock write: %v", err)
				continue
			}
			last = tc
		}
	}()
	log.Printf("[tcloop] listening for external timecode on UDP :%d", port)
	return nil
}

func normalizeTimecode(raw string) string {
	line := strings.TrimSpace(raw)
	if line == "" {
		return ""
	}
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	// Accept HH:MM:SS with optional :FF / ;FF from senders — burn-in shows time only.
	line = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, line)
	if len(line) > 32 {
		line = line[:32]
	}
	if !reTimecodeLine.MatchString(line) {
		return ""
	}
	return stripTimecodeFrames(line)
}

func stripTimecodeFrames(line string) string {
	sep := ":"
	if strings.Contains(line, ";") {
		sep = ";"
	}
	parts := strings.Split(line, sep)
	if len(parts) >= 4 {
		return strings.Join(parts[:3], ":")
	}
	return line
}

func startClockFile(path string, source Source, channelID, udpPort int, stop <-chan struct{}) error {
	if source == SourceExternal {
		port := udpPort
		if port <= 0 {
			port = defaultUDPPort(channelID)
		}
		return startUDPClockFile(path, port, stop)
	}
	return startTODClockFile(path, stop)
}
