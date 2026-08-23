package tcloop

import (
	"fmt"
	"log"
	"net"
	"os"
	"regexp"
	"strings"
	"time"
)

var reTimecodeLine = regexp.MustCompile(`^\d{1,2}:\d{2}:\d{2}([:.;]\d{2})?$`)

func defaultUDPPort(id int) int {
	return 9300 + id
}

func startTODClockFile(path string, stop <-chan struct{}) error {
	write := func() error {
		return os.WriteFile(path, []byte(time.Now().Format("15:04:05")), 0o644)
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
	if err := os.WriteFile(path, []byte("--:--:--:--"), 0o644); err != nil {
		_ = conn.Close()
		return fmt.Errorf("udp clock file: %w", err)
	}
	go func() {
		defer conn.Close()
		buf := make([]byte, 512)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				continue
			}
			tc := normalizeTimecode(string(buf[:n]))
			if tc == "" {
				continue
			}
			if err := os.WriteFile(path, []byte(tc), 0o644); err != nil {
				log.Printf("[tcloop] udp clock write: %v", err)
			}
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
	// Accept "HH:MM:SS:FF" or "HH:MM:SS;FF" or "HH:MM:SS" from the sender as-is.
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
