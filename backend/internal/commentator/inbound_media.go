package commentator

import (
	"bufio"
	"context"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"

	"github.com/hraban/opus"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media/samplebuilder"
)

const (
	inboundVideoW = 1280
	inboundVideoH = 720
	inboundFrame  = inboundVideoW * inboundVideoH * 3 / 2
	micIdleTimeout = 300 * time.Millisecond
)

func (m *Manager) consumeCommentatorMic(ctx context.Context, channelID int, tr *webrtc.TrackRemote, router *AudioRouter) {
	log.Printf("[commentator %d] receiving mic track %s (%s)", channelID, tr.ID(), tr.Codec().MimeType)
	dec, err := opus.NewDecoder(sampleRate, opus.ChannelsMono)
	if err != nil {
		log.Printf("[commentator %d] opus decoder: %v", channelID, err)
		return
	}
	builder := samplebuilder.New(10, tr.Codec().ClockRate)
	pcm := make([]int16, samplesPerFrame*6)
	idleTimer := time.NewTimer(micIdleTimeout)
	defer idleTimer.Stop()
	resetIdle := func() {
		if !idleTimer.Stop() {
			select {
			case <-idleTimer.C:
			default:
			}
		}
		idleTimer.Reset(micIdleTimeout)
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-idleTimer.C:
				router.ClearMic()
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		pkt, _, err := tr.ReadRTP()
		if err != nil {
			if err != io.EOF {
				log.Printf("[commentator %d] mic rtp: %v", channelID, err)
			}
			return
		}
		builder.Push(pkt)
		for {
			sample := builder.Pop()
			if sample == nil {
				break
			}
			n, err := dec.Decode(sample.Data, pcm)
			if err != nil || n <= 0 {
				continue
			}
			router.PushMic(pcm[:n])
			resetIdle()
		}
	}
}

func (m *Manager) consumeCommentatorWebcam(ctx context.Context, channelID int, tr *webrtc.TrackRemote, frames chan<- []byte) {
	log.Printf("[commentator %d] receiving webcam track %s (%s)", channelID, tr.ID(), tr.Codec().MimeType)
	mime := tr.Codec().MimeType
	switch mime {
	case webrtc.MimeTypeH264:
		m.consumeH264Webcam(ctx, channelID, tr, frames)
	default:
		log.Printf("[commentator %d] unsupported webcam codec %s — black frames only", channelID, mime)
		m.emitBlackVideo(ctx, frames)
	}
}

func (m *Manager) consumeH264Webcam(ctx context.Context, channelID int, tr *webrtc.TrackRemote, frames chan<- []byte) {
	builder := samplebuilder.New(10, tr.Codec().ClockRate)
	decCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	pr, pw := io.Pipe()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.runWebcamH264Decoder(decCtx, channelID, pr, frames)
	}()

	for {
		select {
		case <-ctx.Done():
			_ = pw.Close()
			wg.Wait()
			return
		default:
		}
		pkt, _, err := tr.ReadRTP()
		if err != nil {
			_ = pw.Close()
			wg.Wait()
			if err != io.EOF {
				log.Printf("[commentator %d] webcam rtp: %v", channelID, err)
			}
			return
		}
		builder.Push(pkt)
		for {
			sample := builder.Pop()
			if sample == nil {
				break
			}
			if _, err := pw.Write(sample.Data); err != nil {
				_ = pw.Close()
				wg.Wait()
				return
			}
		}
	}
}

func (m *Manager) runWebcamH264Decoder(ctx context.Context, channelID int, r io.Reader, frames chan<- []byte) {
	if m.ffmpegBin == "" {
		m.emitBlackVideo(ctx, frames)
		return
	}
	args := []string{
		"-hide_banner", "-loglevel", "warning",
		"-probesize", "32", "-analyzeduration", "0",
		"-f", "h264", "-i", "pipe:0",
		"-vf", "scale=1280:720",
		"-r", "25",
		"-pix_fmt", "yuv420p",
		"-f", "rawvideo", "pipe:1",
	}
	cmd := exec.CommandContext(ctx, m.ffmpegBin, args...)
	cmd.Stdin = r
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("[commentator %d] webcam decode stdout: %v", channelID, err)
		m.emitBlackVideo(ctx, frames)
		return
	}
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		log.Printf("[commentator %d] webcam decode start: %v", channelID, err)
		m.emitBlackVideo(ctx, frames)
		return
	}
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			log.Printf("[commentator %d] webcam dec %s", channelID, sc.Text())
		}
	}()
	buf := make([]byte, inboundFrame)
	for {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return
		default:
		}
		if _, err := io.ReadFull(stdout, buf); err != nil {
			if ctx.Err() == nil && err != io.EOF {
				log.Printf("[commentator %d] webcam decode read: %v", channelID, err)
			}
			return
		}
		frame := append([]byte(nil), buf...)
		select {
		case frames <- frame:
		case <-ctx.Done():
			return
		}
	}
}

func (m *Manager) emitBlackVideo(ctx context.Context, frames chan<- []byte) {
	frame := make([]byte, inboundFrame)
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			select {
			case frames <- frame:
			case <-ctx.Done():
				return
			}
		}
	}
}
