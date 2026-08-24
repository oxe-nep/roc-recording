package commentator

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
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
	intercom []IntercomSlot,
	intercomTracks []*webrtc.TrackLocalStaticSample,
	stopSilence context.CancelFunc,
) {
	input := strings.TrimSpace(m.channelInput(channelID))
	if input == "" || m.ffmpegBin == "" {
		log.Printf("[commentator %d] no ffmpeg input configured — outbound media uses silence/test pattern only", channelID)
		m.runTestPattern(ctx, videoTrack)
		return
	}

	args := []string{"-hide_banner", "-loglevel", "warning", "-y"}
	args = append(args, ensureDeckLinkChannels(shellSplit(input), 8)...)
	args = append(args, "-analyzeduration", "0", "-probesize", "32")

	filters := []string{
		"[0:v]scale=1280:720,fps=25,format=yuv420p[vout]",
		"[0:a]pan=8c|c0=c0|c1=c1|c2=c2|c3=c3|c4=c4|c5=c5|c6=c6|c7=c7[a8]",
		"[a8]pan=stereo|c0=c0|c1=c1[pgm]",
	}
	audioLabels := []struct {
		label string
		track *webrtc.TrackLocalStaticSample
	}{
		{label: "pgm", track: pgmTrack},
	}
	for i, slot := range intercom {
		if i >= len(intercomTracks) {
			break
		}
		label := fmt.Sprintf("ic%d", slot.ID)
		ch := slot.ID + 1
		filters = append(filters, fmt.Sprintf("[a8]pan=mono|c0=c%d[%s]", ch, label))
		audioLabels = append(audioLabels, struct {
			label string
			track *webrtc.TrackLocalStaticSample
		}{label: label, track: intercomTracks[i]})
	}
	args = append(args, "-filter_complex", strings.Join(filters, ";"))

	args = append(args,
		"-map", "[vout]",
		"-c:v", "libx264",
		"-profile:v", "baseline",
		"-level", "3.1",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-g", "25",
		"-bf", "0",
		"-x264-params", "repeat-headers=1:annexb=1:scenecut=0",
		"-f", "h264",
		"pipe:1",
	)

	type audioPipe struct {
		track  *webrtc.TrackLocalStaticSample
		reader *os.File
		writer *os.File
		fd     int
		stereo bool
	}
	pipes := make([]audioPipe, 0, len(audioLabels))
	nextFD := 3
	for _, al := range audioLabels {
		r, w, err := os.Pipe()
		if err != nil {
			for _, p := range pipes {
				_ = p.writer.Close()
				_ = p.reader.Close()
			}
			log.Printf("[commentator %d] audio pipe: %v", channelID, err)
			m.runTestPattern(ctx, videoTrack)
			return
		}
		stereo := al.label == "pgm"
		bitrate := "64000"
		if stereo {
			bitrate = "128000"
		}
		ch := "1"
		if stereo {
			ch = "2"
		}
		args = append(args,
			"-map", fmt.Sprintf("[%s]", al.label),
			"-c:a", "libopus",
			"-application", "lowdelay",
			"-frame_duration", "20",
			"-ac", ch,
			"-b:a", bitrate,
			"-f", "opus",
			fmt.Sprintf("pipe:%d", nextFD),
		)
		pipes = append(pipes, audioPipe{
			track:  al.track,
			reader: r,
			writer: w,
			fd:     nextFD,
			stereo: stereo,
		})
		nextFD++
	}

	cmd := exec.CommandContext(ctx, m.ffmpegBin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		for _, p := range pipes {
			_ = p.writer.Close()
			_ = p.reader.Close()
		}
		log.Printf("[commentator %d] ffmpeg stdout: %v", channelID, err)
		m.runTestPattern(ctx, videoTrack)
		return
	}
	extra := make([]*os.File, len(pipes))
	for i, p := range pipes {
		// FFmpeg writes encoded opus to pipe:N — pass the write end (not the reader).
		extra[i] = p.writer
	}
	cmd.ExtraFiles = extra

	stderr, err := cmd.StderrPipe()
	if err != nil {
		for _, p := range pipes {
			_ = p.writer.Close()
			_ = p.reader.Close()
		}
		log.Printf("[commentator %d] ffmpeg stderr: %v", channelID, err)
		m.runTestPattern(ctx, videoTrack)
		return
	}
	if err := cmd.Start(); err != nil {
		for _, p := range pipes {
			_ = p.writer.Close()
			_ = p.reader.Close()
		}
		log.Printf("[commentator %d] ffmpeg start: %v", channelID, err)
		m.runTestPattern(ctx, videoTrack)
		return
	}
	for _, p := range pipes {
		_ = p.writer.Close()
	}

	if stopSilence != nil {
		stopSilence()
	}

	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			log.Printf("[commentator %d] in %s", channelID, sc.Text())
		}
	}()

	go m.pipeH264ToTrack(ctx, stdout, videoTrack)
	for _, p := range pipes {
		go m.pipeOpusToTrack(ctx, p.reader, p.track)
	}

	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		log.Printf("[commentator %d] ffmpeg inbound exited: %v", channelID, err)
	}
	for _, p := range pipes {
		_ = p.reader.Close()
	}
}

func ensureDeckLinkChannels(args []string, n int) []string {
	if n != 2 && n != 8 && n != 16 {
		n = 8
	}
	if !decklinkInput(args) {
		return args
	}
	for i := 0; i < len(args); i++ {
		if args[i] == "-channels" || args[i] == "-audio_channels" {
			return args
		}
	}
	val := strconv.Itoa(n)
	for i := 0; i < len(args); i++ {
		if args[i] == "-i" {
			out := append([]string{}, args[:i]...)
			out = append(out, "-channels", val)
			out = append(out, args[i:]...)
			return out
		}
	}
	return append(append([]string{}, args...), "-channels", val)
}

func decklinkInput(args []string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "-f" && i+1 < len(args) && strings.EqualFold(args[i+1], "decklink") {
			return true
		}
	}
	return false
}

// runSilenceFallback keeps opus tracks alive until FFmpeg supplies real PCM.
func (m *Manager) runSilenceFallback(ctx context.Context, pgm *webrtc.TrackLocalStaticSample, intercom []*webrtc.TrackLocalStaticSample) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	silent := media.Sample{Data: []byte{0xf8, 0xff, 0xfe}, Duration: 20 * time.Millisecond}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = pgm.WriteSample(silent)
			for _, t := range intercom {
				_ = t.WriteSample(silent)
			}
		}
	}
}

func (m *Manager) runTestPattern(ctx context.Context, videoTrack *webrtc.TrackLocalStaticSample) {
	if m.ffmpegBin == "" {
		return
	}
	args := []string{
		"-hide_banner", "-loglevel", "warning",
		"-f", "lavfi", "-i", "testsrc=size=1280x720:rate=25",
		"-pix_fmt", "yuv420p",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-g", "25",
		"-bf", "0",
		"-x264-params", "repeat-headers=1:annexb=1",
		"-f", "h264", "pipe:1",
	}
	cmd := exec.CommandContext(ctx, m.ffmpegBin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return
	}
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			log.Printf("[commentator] testsrc %s", sc.Text())
		}
	}()
	writer := newH264TrackWriter(videoTrack)
	tmp := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return
		default:
		}
		n, err := stdout.Read(tmp)
		if n > 0 {
			writer.feed(tmp[:n])
		}
		if err != nil {
			return
		}
	}
}

func (m *Manager) pipeH264ToTrack(ctx context.Context, r io.Reader, track *webrtc.TrackLocalStaticSample) {
	writer := newH264TrackWriter(track)
	tmp := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := r.Read(tmp)
		if n > 0 {
			writer.feed(tmp[:n])
		}
		if err != nil {
			if err != io.EOF && ctx.Err() == nil {
				log.Printf("[commentator] h264 pipe: %v", err)
			}
			return
		}
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
