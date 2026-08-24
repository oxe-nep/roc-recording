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

// runFFmpegOutbound keeps webcam → DeckLink alive across device/format blips.
func (m *Manager) runFFmpegOutbound(ctx context.Context, channelID int, router *AudioRouter, videoFrames <-chan []byte) {
	if m.playout == nil {
		log.Printf("[commentator %d] no playout bridge — DeckLink OUT disabled", channelID)
		return
	}
	if m.ffmpegBin == "" {
		return
	}

	fails := 0
	for {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		err := m.runFFmpegOutboundOnce(ctx, channelID, router, videoFrames)
		if ctx.Err() != nil {
			return
		}
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
		log.Printf("[commentator %d] decklink out exited after %s: %v (fail #%d) – retry in %s",
			channelID, uptime.Round(time.Millisecond), err, fails, delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

func (m *Manager) runFFmpegOutboundOnce(ctx context.Context, channelID int, router *AudioRouter, videoFrames <-chan []byte) error {
	device, formatCode, err := m.OutputSink(channelID)
	if err != nil {
		return err
	}
	openDevice := m.playout.ResolveOpenDevice(device)
	if alt := m.playout.LookupDeviceOpen(device); alt != "" {
		openDevice = alt
	}
	w, h, fps, interlaced, err := m.playout.OutputTiming(formatCode)
	if err != nil || w <= 0 || h <= 0 || fps <= 0 {
		return fmt.Errorf("output timing: %w", err)
	}

	videoR, videoW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("video pipe: %w", err)
	}
	audioR, audioW, err := os.Pipe()
	if err != nil {
		closePipes(videoW, videoR, nil, nil)
		return fmt.Errorf("audio pipe: %w", err)
	}

	vfilter := deckLinkVideoFilter(w, h, fps, interlaced)
	afilter := fmt.Sprintf("[1:a]asetpts=PTS-STARTPTS,asetnsamples=n=%d:p=0[aout]", samplesPerFrame)
	filter := vfilter + ";" + afilter

	// Webcam is for monitoring — keep DeckLink OUT buffers small for lip-sync with mic.
	args := []string{"-hide_banner", "-loglevel", "warning", "-y", "-fflags", "nobuffer+genpts", "-flags", "low_delay"}
	args = append(args,
		"-thread_queue_size", "2",
		"-f", "rawvideo", "-pix_fmt", "yuv420p",
		"-s", fmt.Sprintf("%dx%d", inboundVideoW, inboundVideoH),
		"-r", fmt.Sprintf("%g", inboundVideoFPS),
		"-i", "pipe:0",
		"-thread_queue_size", "2",
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
	args = append(args, "-preroll", "0.05", "-f", "decklink", openDevice)

	cmd := exec.CommandContext(ctx, m.ffmpegBin, args...)
	cmd.Stdin = videoR
	cmd.ExtraFiles = []*os.File{audioR}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		closePipes(videoW, videoR, audioW, audioR)
		return fmt.Errorf("stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		closePipes(videoW, videoR, audioW, audioR)
		return fmt.Errorf("start: %w", err)
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

	err = cmd.Wait()
	cancel()
	closePipes(nil, videoR, nil, audioR)
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (m *Manager) feedOutboundVideo(ctx context.Context, w *os.File, frames <-chan []byte) {
	defer w.Close()
	black := yuv420BlackFrame(inboundVideoW, inboundVideoH)
	ticker := time.NewTicker(time.Duration(float64(time.Second) / inboundVideoFPS))
	defer ticker.Stop()
	var latest []byte
	var loggedWait, loggedLive bool
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-frames:
			if ok && len(frame) == inboundFrame {
				latest = frame
				if !loggedLive {
					loggedLive = true
					log.Printf("[commentator] DeckLink OUT receiving decoded webcam frames")
				}
			}
		case <-ticker.C:
			frame := black
			if len(latest) == inboundFrame {
				frame = latest
			} else if !loggedWait {
				loggedWait = true
				log.Printf("[commentator] DeckLink OUT waiting for webcam frames (sending black)")
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
