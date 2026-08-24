package commentator

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hraban/opus"
	"github.com/pion/rtcp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media/samplebuilder"
)

const (
	inboundVideoW  = 1280
	inboundVideoH  = 720
	inboundFrame   = inboundVideoW * inboundVideoH * 3 / 2
	micIdleTimeout = 300 * time.Millisecond
)

func (m *Manager) consumeCommentatorMic(ctx context.Context, channelID int, tr *webrtc.TrackRemote, router *AudioRouter) {
	log.Printf("[commentator %d] receiving mic track %s (%s)", channelID, tr.ID(), tr.Codec().MimeType)
	// Browsers usually send stereo Opus; downmix to mono for DeckLink routing.
	dec, err := opus.NewDecoder(sampleRate, 2)
	if err != nil {
		log.Printf("[commentator %d] opus decoder: %v", channelID, err)
		return
	}
	builder := samplebuilder.New(10, &codecs.OpusPacket{}, tr.Codec().ClockRate)
	pcmStereo := make([]int16, samplesPerFrame*2)
	mono := make([]int16, samplesPerFrame)
	loggedMic := false
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
			n, err := dec.Decode(sample.Data, pcmStereo)
			if err != nil || n <= 0 {
				continue
			}
			for i := 0; i < n && i < samplesPerFrame; i++ {
				l := int32(pcmStereo[i*2])
				r := int32(pcmStereo[i*2+1])
				mono[i] = int16((l + r) / 2)
			}
			router.PushMic(mono[:n])
			resetIdle()
			if !loggedMic {
				loggedMic = true
				log.Printf("[commentator %d] mic audio flowing (%d samples/frame)", channelID, n)
			}
		}
	}
}

func (m *Manager) consumeCommentatorWebcam(ctx context.Context, channelID int, tr *webrtc.TrackRemote, pc *webrtc.PeerConnection, frames chan<- []byte) {
	log.Printf("[commentator %d] receiving webcam track %s (%s)", channelID, tr.ID(), tr.Codec().MimeType)
	go m.requestPLI(ctx, pc, tr)
	mime := strings.ToLower(tr.Codec().MimeType)
	switch {
	case strings.Contains(mime, "h264"):
		m.consumeH264Webcam(ctx, channelID, tr, frames)
	case strings.Contains(mime, "vp8"):
		m.consumeVP8Webcam(ctx, channelID, tr, frames)
	default:
		log.Printf("[commentator %d] unsupported webcam codec %s — black frames only", channelID, tr.Codec().MimeType)
		m.emitBlackVideo(ctx, frames)
	}
}

func (m *Manager) requestPLI(ctx context.Context, pc *webrtc.PeerConnection, tr *webrtc.TrackRemote) {
	if pc == nil {
		return
	}
	send := func() {
		_ = pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(tr.SSRC())}})
	}
	send()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			send()
		}
	}
}

func (m *Manager) consumeH264Webcam(ctx context.Context, channelID int, tr *webrtc.TrackRemote, frames chan<- []byte) {
	builder := samplebuilder.New(20, &codecs.H264Packet{}, tr.Codec().ClockRate)

	// Buffer until first IDR so the annex-B decoder can start cleanly.
	var idr []byte
	var spsPps []byte
waitIDR:
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		pkt, _, err := tr.ReadRTP()
		if err != nil {
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
			nal := toAnnexB(sample.Data)
			switch h264NALType(nal) {
			case 7, 8:
				spsPps = append(spsPps, nal...)
			case 5:
				idr = append(append([]byte(nil), spsPps...), nal...)
				log.Printf("[commentator %d] webcam H264 IDR — starting decoder", channelID)
				break waitIDR
			}
		}
	}

	decCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	pr, pw := io.Pipe()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.runWebcamH264Decoder(decCtx, channelID, pr, frames)
	}()

	if _, err := pw.Write(idr); err != nil {
		_ = pw.Close()
		wg.Wait()
		return
	}
	log.Printf("[commentator %d] webcam H264 samples → decoder", channelID)

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
			if _, err := pw.Write(toAnnexB(sample.Data)); err != nil {
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
		"-probesize", "32768", "-analyzeduration", "0",
		"-fflags", "nobuffer+genpts+flush_packets",
		"-flags", "low_delay",
		"-f", "h264", "-i", "pipe:0",
		"-vf", "scale=1280:720:flags=fast_bilinear",
		"-r", "25",
		"-pix_fmt", "yuv420p",
		"-f", "rawvideo", "pipe:1",
	}
	m.runWebcamRawDecoder(ctx, channelID, "h264", args, r, frames)
}

func (m *Manager) consumeVP8Webcam(ctx context.Context, channelID int, tr *webrtc.TrackRemote, frames chan<- []byte) {
	builder := samplebuilder.New(20, &codecs.VP8Packet{}, tr.Codec().ClockRate)

	// Do not start FFmpeg until we have a real keyframe — otherwise:
	// "Discarding interframe without a prior keyframe" and black DeckLink.
	var (
		width  uint16 = inboundVideoW
		height uint16 = inboundVideoH
		first  []byte
	)
waitKey:
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		pkt, _, err := tr.ReadRTP()
		if err != nil {
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
			if !vp8IsKeyframe(sample.Data) {
				continue
			}
			if w, h, ok := vp8KeyframeSize(sample.Data); ok {
				width, height = w, h
			}
			first = append([]byte(nil), sample.Data...)
			log.Printf("[commentator %d] webcam VP8 keyframe %dx%d — starting decoder", channelID, width, height)
			break waitKey
		}
	}

	decCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.runWebcamVP8Decoder(decCtx, channelID, pr, frames)
	}()

	if err := writeIVFFileHeader(pw, width, height); err != nil {
		_ = pw.Close()
		wg.Wait()
		return
	}
	var pts uint64
	if err := writeIVFFrame(pw, first, pts); err != nil {
		_ = pw.Close()
		wg.Wait()
		return
	}
	pts += 3600
	log.Printf("[commentator %d] webcam VP8 samples → decoder", channelID)

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
			if len(sample.Data) == 0 {
				continue
			}
			if err := writeIVFFrame(pw, sample.Data, pts); err != nil {
				_ = pw.Close()
				wg.Wait()
				return
			}
			pts += 3600
		}
	}
}

func vp8IsKeyframe(data []byte) bool {
	return len(data) >= 10 && data[0]&0x1 == 0
}

func vp8KeyframeSize(data []byte) (w, h uint16, ok bool) {
	if !vp8IsKeyframe(data) {
		return 0, 0, false
	}
	raw := uint32(data[6]) | uint32(data[7])<<8 | uint32(data[8])<<16 | uint32(data[9])<<24
	w = uint16(raw & 0x3FFF)
	h = uint16((raw >> 16) & 0x3FFF)
	return w, h, w > 0 && h > 0
}

func writeIVFFileHeader(w io.Writer, width, height uint16) error {
	hdr := make([]byte, 32)
	copy(hdr[0:], "DKIF")
	binary.LittleEndian.PutUint16(hdr[4:], 0)  // version
	binary.LittleEndian.PutUint16(hdr[6:], 32) // header size
	copy(hdr[8:], "VP80")
	binary.LittleEndian.PutUint16(hdr[12:], width)
	binary.LittleEndian.PutUint16(hdr[14:], height)
	binary.LittleEndian.PutUint32(hdr[16:], 90000) // timebase den
	binary.LittleEndian.PutUint32(hdr[20:], 1)     // timebase num
	binary.LittleEndian.PutUint32(hdr[24:], 0)     // frame count
	binary.LittleEndian.PutUint32(hdr[28:], 0)     // unused
	_, err := w.Write(hdr)
	return err
}

func writeIVFFrame(w io.Writer, data []byte, pts uint64) error {
	hdr := make([]byte, 12)
	binary.LittleEndian.PutUint32(hdr[0:], uint32(len(data)))
	binary.LittleEndian.PutUint64(hdr[4:], pts)
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

func (m *Manager) runWebcamVP8Decoder(ctx context.Context, channelID int, r io.Reader, frames chan<- []byte) {
	if m.ffmpegBin == "" {
		m.emitBlackVideo(ctx, frames)
		return
	}
	args := []string{
		"-hide_banner", "-loglevel", "warning",
		"-probesize", "32768", "-analyzeduration", "0",
		"-fflags", "nobuffer+flush_packets",
		"-flags", "low_delay",
		"-f", "ivf", "-i", "pipe:0",
		"-vf", "scale=1280:720:flags=fast_bilinear",
		"-r", "25",
		"-pix_fmt", "yuv420p",
		"-f", "rawvideo", "pipe:1",
	}
	m.runWebcamRawDecoder(ctx, channelID, "vp8", args, r, frames)
}

func (m *Manager) runWebcamRawDecoder(ctx context.Context, channelID int, label string, args []string, r io.Reader, frames chan<- []byte) {
	cmd := exec.CommandContext(ctx, m.ffmpegBin, args...)
	cmd.Stdin = r
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("[commentator %d] webcam %s stdout: %v", channelID, label, err)
		m.emitBlackVideo(ctx, frames)
		return
	}
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		log.Printf("[commentator %d] webcam %s start: %v", channelID, label, err)
		m.emitBlackVideo(ctx, frames)
		return
	}
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			log.Printf("[commentator %d] webcam %s %s", channelID, label, sc.Text())
		}
	}()
	buf := make([]byte, inboundFrame)
	var outFrames uint64
	for {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return
		default:
		}
		if _, err := io.ReadFull(stdout, buf); err != nil {
			if ctx.Err() == nil && err != io.EOF {
				log.Printf("[commentator %d] webcam %s read: %v (decoded %d frames)", channelID, label, err, outFrames)
			}
			return
		}
		frame := append([]byte(nil), buf...)
		n := atomic.AddUint64(&outFrames, 1)
		if n == 1 {
			log.Printf("[commentator %d] webcam %s decoded frames → DeckLink", channelID, label)
		}
		select {
		case frames <- frame:
		case <-ctx.Done():
			return
		default:
			// Drop frame if DeckLink consumer is behind (channel buffer full).
		}
	}
}

func yuv420BlackFrame(w, h int) []byte {
	frame := make([]byte, w*h*3/2)
	// Y = 16 (limited-range black), U/V = 128 — all-zero YUV is green on DeckLink.
	for i := 0; i < w*h; i++ {
		frame[i] = 16
	}
	for i := w * h; i < len(frame); i++ {
		frame[i] = 128
	}
	return frame
}

func (m *Manager) emitBlackVideo(ctx context.Context, frames chan<- []byte) {
	frame := yuv420BlackFrame(inboundVideoW, inboundVideoH)
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
