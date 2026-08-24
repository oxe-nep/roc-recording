package commentator

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"time"
)

const inboundVideoFPS = 25.0

func (m *Manager) runFFmpegOutbound(ctx context.Context, channelID int, router *AudioRouter, videoFrames <-chan []byte) {
	if m.playout == nil {
		log.Printf("[commentator %d] no playout bridge — DeckLink OUT disabled", channelID)
		return
	}
	device, formatCode, err := m.OutputSink(channelID)
	if err != nil {
		log.Printf("[commentator %d] output sink: %v", channelID, err)
		return
	}
	openDevice := m.playout.ResolveOpenDevice(device)
	if alt := m.playout.LookupDeviceOpen(device); alt != "" {
		openDevice = alt
	}
	w, h, fps, interlaced, err := m.playout.OutputTiming(formatCode)
	if err != nil || w <= 0 || h <= 0 || fps <= 0 {
		log.Printf("[commentator %d] output timing: %v", channelID, err)
		return
	}

	videoR, videoW, err := os.Pipe()
	if err != nil {
		log.Printf("[commentator %d] video pipe: %v", channelID, err)
		return
	}
	audioR, audioW, err := os.Pipe()
	if err != nil {
		_ = videoW.Close()
		_ = videoR.Close()
		log.Printf("[commentator %d] audio pipe: %v", channelID, err)
		return
	}

	vfilter := deckLinkVideoFilter(w, h, fps, interlaced)
	afilter := fmt.Sprintf("[1:a]asetpts=PTS-STARTPTS,asetnsamples=n=%d:p=0[aout]", samplesPerFrame)
	filter := vfilter + ";" + afilter

	args := []string{"-hide_banner", "-loglevel", "warning", "-y", "-fflags", "+genpts"}
	args = append(args,
		"-f", "rawvideo", "-pix_fmt", "yuv420p",
		"-s", fmt.Sprintf("%dx%d", inboundVideoW, inboundVideoH),
		"-r", fmt.Sprintf("%g", inboundVideoFPS),
		"-i", "pipe:0",
		"-f", "s16le", "-ac", "8", "-ar", strconv.Itoa(sampleRate),
		"-i", "pipe:3",
		"-filter_complex", filter,
		"-map", "[vout]",
		"-map", "[aout]",
		"-c:v", "v210",
		"-c:a", "pcm_s16le",
		"-ar", strconv.Itoa(sampleRate),
		"-ac", "8",
		"-fps_mode", "cfr",
		"-r", fmt.Sprintf("%g", fps),
		"-s", fmt.Sprintf("%dx%d", w, h),
	)
	if interlaced {
		args = append(args, "-flags", "+ilme+ildct", "-field_order", "tt")
	}
	if formatCode != "" && !isAllDigits(formatCode) {
		args = append(args, "-format_code", formatCode)
	}
	args = append(args, "-preroll", "0.5", "-f", "decklink", openDevice)

	cmd := exec.CommandContext(ctx, m.ffmpegBin, args...)
	cmd.Stdin = videoR
	cmd.ExtraFiles = []*os.File{audioR}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		closePipes(videoW, videoR, audioW, audioR)
		log.Printf("[commentator %d] decklink stderr: %v", channelID, err)
		return
	}
	if err := cmd.Start(); err != nil {
		closePipes(videoW, videoR, audioW, audioR)
		log.Printf("[commentator %d] decklink start: %v", channelID, err)
		return
	}
	log.Printf("[commentator %d] DeckLink OUT → %q format=%s (%dx%d @ %g interlaced=%v)", channelID, openDevice, formatCode, w, h, fps, interlaced)

	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			log.Printf("[commentator %d] decklink %s", channelID, sc.Text())
		}
	}()

	outCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go m.feedOutboundVideo(outCtx, videoW, videoFrames)
	go m.feedOutboundAudio(outCtx, audioW, router)

	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		log.Printf("[commentator %d] decklink exited: %v", channelID, err)
	}
	cancel()
	closePipes(nil, videoR, nil, audioR)
}

func (m *Manager) feedOutboundVideo(ctx context.Context, w *os.File, frames <-chan []byte) {
	defer w.Close()
	black := make([]byte, inboundFrame)
	ticker := time.NewTicker(time.Duration(float64(time.Second) / inboundVideoFPS))
	defer ticker.Stop()
	var latest []byte
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-frames:
			if ok && len(frame) == inboundFrame {
				latest = frame
			}
		case <-ticker.C:
			frame := black
			if len(latest) == inboundFrame {
				frame = latest
			}
			if _, err := w.Write(frame); err != nil {
				return
			}
		}
	}
}

func (m *Manager) feedOutboundAudio(ctx context.Context, w *os.File, router *AudioRouter) {
	defer w.Close()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.Write(router.Frame8ch()); err != nil {
				return
			}
		}
	}
}

func closePipes(vw, vr, aw, ar *os.File) {
	for _, f := range []*os.File{vw, vr, aw, ar} {
		if f != nil {
			_ = f.Close()
		}
	}
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// deckLinkVideoFilter matches playout decode: Hi50/Hi25 need tinterlace + field rate.
func deckLinkVideoFilter(w, h int, fps float64, interlaced bool) string {
	if interlaced {
		return fmt.Sprintf(
			"[0:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setpts=PTS-STARTPTS,fps=%g,tinterlace=interleave_top,format=yuv422p10le[vout]",
			w, h, w, h, fps*2,
		)
	}
	return fmt.Sprintf(
		"[0:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setpts=PTS-STARTPTS,fps=%g,format=yuv422p10le[vout]",
		w, h, w, h, fps,
	)
}
