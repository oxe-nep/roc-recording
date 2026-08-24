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

	"github.com/hraban/opus"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const (
	ffmpegRestartDelay = 3 * time.Second
	ffmpegStableAfter  = 30 * time.Second
)

// runFFmpegInbound keeps DeckLink → WebRTC alive across signal loss / re-routes.
func (m *Manager) runFFmpegInbound(
	ctx context.Context,
	channelID int,
	videoTrack *webrtc.TrackLocalStaticSample,
	pgmTrack *webrtc.TrackLocalStaticSample,
	intercom []IntercomSlot,
	intercomTracks []*webrtc.TrackLocalStaticSample,
	_ context.CancelFunc, // legacy; silence is owned by this loop
) {
	input := strings.TrimSpace(m.channelInput(channelID))
	if input == "" || m.ffmpegBin == "" {
		log.Printf("[commentator %d] no ffmpeg input configured — outbound media uses silence/test pattern only", channelID)
		m.runTestPattern(ctx, videoTrack)
		return
	}

	var silenceCancel context.CancelFunc
	startSilence := func() {
		if silenceCancel != nil {
			return
		}
		sctx, cancel := context.WithCancel(ctx)
		silenceCancel = cancel
		go m.runSilenceFallback(sctx, pgmTrack, intercomTracks)
	}
	stopSilence := func() {
		if silenceCancel != nil {
			silenceCancel()
			silenceCancel = nil
		}
	}
	startSilence()
	defer stopSilence()

	fails := 0
	for {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		err := m.runFFmpegInboundOnce(ctx, channelID, input, videoTrack, pgmTrack, intercom, intercomTracks, stopSilence)
		if ctx.Err() != nil {
			return
		}
		startSilence()
		uptime := time.Since(start)
		if uptime >= ffmpegStableAfter {
			fails = 0
		} else {
			fails++
		}
		delay := ffmpegRestartDelay
		if fails > 5 {
			delay = 15 * time.Second
		}
		log.Printf("[commentator %d] ffmpeg inbound exited after %s: %v (fail #%d) – retry in %s",
			channelID, uptime.Round(time.Millisecond), err, fails, delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

func (m *Manager) runFFmpegInboundOnce(
	ctx context.Context,
	channelID int,
	input string,
	videoTrack *webrtc.TrackLocalStaticSample,
	pgmTrack *webrtc.TrackLocalStaticSample,
	intercom []IntercomSlot,
	intercomTracks []*webrtc.TrackLocalStaticSample,
	onStarted func(),
) error {
	args := []string{"-hide_banner", "-loglevel", "warning", "-y"}
	args = append(args, ensureDeckLinkChannels(shellSplit(input), 8)...)
	args = append(args, "-analyzeduration", "0", "-probesize", "32")

	filters := []string{
		// DeckLink Hi50/Hi25 are interlaced — deinterlace before scale or the browser shows striped frames.
		"[0:v]yadif=0:-1:0,scale=1280:720,fps=25,format=yuv420p[vout]",
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
			return fmt.Errorf("audio pipe: %w", err)
		}
		stereo := al.label == "pgm"
		ch := "1"
		if stereo {
			ch = "2"
		}
		// Raw PCM → encode Opus packets in Go. FFmpeg -f opus is Ogg and unusable for WebRTC.
		args = append(args,
			"-map", fmt.Sprintf("[%s]", al.label),
			"-c:a", "pcm_s16le",
			"-ac", ch,
			"-ar", "48000",
			"-f", "s16le",
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
		return fmt.Errorf("stdout: %w", err)
	}
	extra := make([]*os.File, len(pipes))
	for i, p := range pipes {
		// FFmpeg writes PCM to pipe:N — pass the write end (not the reader).
		extra[i] = p.writer
	}
	cmd.ExtraFiles = extra

	stderr, err := cmd.StderrPipe()
	if err != nil {
		for _, p := range pipes {
			_ = p.writer.Close()
			_ = p.reader.Close()
		}
		return fmt.Errorf("stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		for _, p := range pipes {
			_ = p.writer.Close()
			_ = p.reader.Close()
		}
		return fmt.Errorf("start: %w", err)
	}
	for _, p := range pipes {
		_ = p.writer.Close()
	}

	if onStarted != nil {
		onStarted()
	}
	log.Printf("[commentator %d] ffmpeg inbound started", channelID)

	pipeCtx, pipeCancel := context.WithCancel(ctx)
	defer pipeCancel()

	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			log.Printf("[commentator %d] in %s", channelID, sc.Text())
		}
	}()

	go m.pipeH264ToTrack(pipeCtx, stdout, videoTrack)
	for _, p := range pipes {
		go m.pipePCMToOpusTrack(pipeCtx, p.reader, p.track, p.stereo)
	}

	err = cmd.Wait()
	pipeCancel()
	for _, p := range pipes {
		_ = p.reader.Close()
	}
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
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

func (m *Manager) pipePCMToOpusTrack(ctx context.Context, r io.Reader, track *webrtc.TrackLocalStaticSample, stereo bool) {
	if r == nil {
		return
	}
	channels := 1
	if stereo {
		channels = 2
	}
	enc, err := opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
	if err != nil {
		log.Printf("[commentator] opus encoder: %v", err)
		return
	}
	_ = enc.SetBitrate(64000)
	if stereo {
		_ = enc.SetBitrate(128000)
	}
	frameSamples := samplesPerFrame * channels
	pcmBytes := make([]byte, frameSamples*2)
	pcm := make([]int16, frameSamples)
	out := make([]byte, 1500)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if _, err := io.ReadFull(r, pcmBytes); err != nil {
			return
		}
		for i := 0; i < frameSamples; i++ {
			pcm[i] = int16(pcmBytes[i*2]) | int16(pcmBytes[i*2+1])<<8
		}
		n, err := enc.Encode(pcm, out)
		if err != nil || n <= 0 {
			continue
		}
		_ = track.WriteSample(media.Sample{Data: append([]byte(nil), out[:n]...), Duration: 20 * time.Millisecond})
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
