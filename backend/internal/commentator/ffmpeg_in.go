package commentator

import (
	"bufio"
	"context"
	"io"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

func (m *Manager) runFFmpegInbound(
	ctx context.Context,
	channelID int,
	videoTrack *webrtc.TrackLocalStaticSample,
	pgmTrack *webrtc.TrackLocalStaticSample,
	intercomTracks []*webrtc.TrackLocalStaticSample,
) {
	input := strings.TrimSpace(m.channelInput(channelID))
	if input == "" || m.ffmpegBin == "" {
		log.Printf("[commentator %d] no ffmpeg input configured — outbound media uses silence/test pattern only", channelID)
		m.runTestPattern(ctx, videoTrack)
		return
	}

	args := []string{"-hide_banner", "-loglevel", "warning", "-y"}
	args = append(args, shellSplit(input)...)
	args = append(args,
		"-analyzeduration", "0",
		"-probesize", "32",
		"-filter_complex", "[0:v]scale=1280:720,fps=25,format=yuv420p[vout]",
		"-map", "[vout]",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-g", "25",
		"-bf", "0",
		"-f", "h264",
		"pipe:1",
	)
	cmd := exec.CommandContext(ctx, m.ffmpegBin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("[commentator %d] ffmpeg stdout: %v", channelID, err)
		m.runTestPattern(ctx, videoTrack)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Printf("[commentator %d] ffmpeg stderr: %v", channelID, err)
		m.runTestPattern(ctx, videoTrack)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("[commentator %d] ffmpeg start: %v", channelID, err)
		m.runTestPattern(ctx, videoTrack)
		return
	}

	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			log.Printf("[commentator %d] %s", channelID, sc.Text())
		}
	}()

	go m.pipeH264ToTrack(ctx, stdout, videoTrack)

	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		log.Printf("[commentator %d] ffmpeg exited: %v", channelID, err)
	}
}

func (m *Manager) runTestPattern(ctx context.Context, videoTrack *webrtc.TrackLocalStaticSample) {
	// Minimal keepalive — real video arrives once DeckLink FFmpeg path is active.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = videoTrack.WriteSample(media.Sample{
				Data:     []byte{0x00, 0x00, 0x00, 0x01, 0x09, 0xf0},
				Duration: 2 * time.Second,
			})
		}
	}
}

func (m *Manager) pipeH264ToTrack(ctx context.Context, r io.Reader, track *webrtc.TrackLocalStaticSample) {
	buf := make([]byte, 0, 256*1024)
	tmp := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			buf = flushH264AnnexB(buf, track)
		}
		if err != nil {
			if err != io.EOF && ctx.Err() == nil {
				log.Printf("[commentator] h264 pipe: %v", err)
			}
			return
		}
	}
}

func flushH264AnnexB(buf []byte, track *webrtc.TrackLocalStaticSample) []byte {
	for {
		start := findAnnexBStart(buf, 0)
		if start < 0 {
			return buf
		}
		next := findAnnexBStart(buf, start+3)
		if next < 0 {
			return buf
		}
		nal := append([]byte(nil), buf[start:next]...)
		_ = track.WriteSample(media.Sample{Data: nal, Duration: 40 * time.Millisecond})
		buf = buf[next:]
	}
}

func findAnnexBStart(buf []byte, from int) int {
	for i := from; i+3 < len(buf); i++ {
		if buf[i] == 0 && buf[i+1] == 0 && buf[i+2] == 1 {
			return i
		}
		if i+4 < len(buf) && buf[i] == 0 && buf[i+1] == 0 && buf[i+2] == 0 && buf[i+3] == 1 {
			return i
		}
	}
	return -1
}

func (m *Manager) pipeOpusToTrack(ctx context.Context, r io.Reader, track *webrtc.TrackLocalStaticSample) {
	if r == nil {
		return
	}
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := r.Read(buf)
		if n > 0 {
			_ = track.WriteSample(media.Sample{Data: append([]byte(nil), buf[:n]...), Duration: 20 * time.Millisecond})
		}
		if err != nil {
			return
		}
	}
}

func (m *Manager) channelInput(id int) string {
	if m.channelInputs == nil {
		return ""
	}
	return m.channelInputs[id]
}

func shellSplit(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	var cur strings.Builder
	inQuote := rune(0)
	for _, r := range s {
		switch {
		case inQuote != 0:
			if r == inQuote {
				inQuote = 0
				continue
			}
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			inQuote = r
		case r == ' ' || r == '\t':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
