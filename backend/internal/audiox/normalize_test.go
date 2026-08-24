package audiox

import (
	"strings"
	"testing"
)

func TestPanTo8DoesNotReferenceMissingChannels(t *testing.T) {
	got := PanTo8(2)
	if got != "pan=8c|c0=c0|c1=c1" {
		t.Fatalf("got %q", got)
	}
	if PanTo8(8) != Discrete8Pan {
		t.Fatalf("8ch identity: %q vs %q", PanTo8(8), Discrete8Pan)
	}
}

func TestParseAudioStreamsEightMono(t *testing.T) {
	banner := `
Stream #0:0: Video: h264, 1920x1080
Stream #0:1: Audio: aac (LC), 48000 Hz, mono, fltp, 64 kb/s
Stream #0:2: Audio: aac (LC), 48000 Hz, mono, fltp, 64 kb/s
Stream #0:3: Audio: aac (LC), 48000 Hz, mono, fltp, 64 kb/s
Stream #0:4: Audio: aac (LC), 48000 Hz, mono, fltp, 64 kb/s
Stream #0:5: Audio: aac (LC), 48000 Hz, mono, fltp, 64 kb/s
Stream #0:6: Audio: aac (LC), 48000 Hz, mono, fltp, 64 kb/s
Stream #0:7: Audio: aac (LC), 48000 Hz, mono, fltp, 64 kb/s
Stream #0:8: Audio: aac (LC), 48000 Hz, mono, fltp, 64 kb/s
`
	chs := ParseAudioStreams(banner)
	if len(chs) != 8 {
		t.Fatalf("streams=%d %v", len(chs), chs)
	}
	for i, n := range chs {
		if n != 1 {
			t.Fatalf("stream %d channels=%d", i, n)
		}
	}
}

func TestParseAudioStreamsWrappedLines(t *testing.T) {
	banner := `
    Stream #0:0[0x1]: Video: prores
    Stream #0:1[0x2](eng):
    Audio: pcm_s24le, 48000 Hz, 1 channels, s32 (24 bit)
    Stream #0:2[0x3](eng):
    Audio: pcm_s24le, 48000 Hz, 1 channels, s32 (24 bit)
`
	chs := ParseAudioStreams(banner)
	if len(chs) != 2 || chs[0] != 1 || chs[1] != 1 {
		t.Fatalf("got %v", chs)
	}
}

func TestParseAudioStreams8chAndStereo(t *testing.T) {
	if n := ParseAudioStreams("Stream #0:1: Audio: pcm_s16le, 48000 Hz, 8 channels, s16\n"); len(n) != 1 || n[0] != 8 {
		t.Fatalf("got %v", n)
	}
	if n := ParseAudioStreams("Stream #0:1[0x101]: Audio: aac, 48000 Hz, stereo, fltp\n"); len(n) != 1 || n[0] != 2 {
		t.Fatalf("got %v", n)
	}
	if n := ParseAudioStreams("Stream #0:1: Audio: pcm_s16le, 48000 Hz, 7.1, s16\n"); len(n) != 1 || n[0] != 8 {
		t.Fatalf("got %v", n)
	}
}

func TestParseFfprobeChannelCSVIgnoresWarnings(t *testing.T) {
	raw := "ffprobe version\n[mov @ 0x1] Using non-standard frame rate\n1\n1\n1\n1\n1\n1\n1\n1\n"
	chs := ParseFfprobeChannelCSV(raw)
	if len(chs) != 8 {
		t.Fatalf("got %v", chs)
	}
}

func TestLinkPadNoCommaAfterLabel(t *testing.T) {
	if g := LinkPad("[a8]", "asplit=2[aprevsrc][ameter];"); g != "[a8]asplit=2[aprevsrc][ameter];" {
		t.Fatalf("got %q", g)
	}
	if g := LinkPad("[a8]", ",aresample=48000[a]"); g != "[a8]aresample=48000[a]" {
		t.Fatalf("got %q", g)
	}
}

func TestFileTo8SingleStereo(t *testing.T) {
	g, pad := FileTo8(0, []int{2})
	if pad != "[a8]" {
		t.Fatalf("pad %q", pad)
	}
	if g != "[0:a]aresample=48000,pan=8c|c0=c0|c1=c1[a8]" {
		t.Fatalf("graph %q", g)
	}
}

func TestFileTo8EightMonoMerges(t *testing.T) {
	g, _ := FileTo8(0, []int{1, 1, 1, 1, 1, 1, 1, 1})
	for _, part := range []string{"[0:a:0]", "[0:a:7]", "amerge=inputs=8", "aformat=channel_layouts=7.1[a8]"} {
		if !strings.Contains(g, part) {
			t.Fatalf("missing %q in %q", part, g)
		}
	}
}

func TestFileTo8FourStereo(t *testing.T) {
	g, _ := FileTo8(0, []int{2, 2, 2, 2})
	for _, part := range []string{"asplit=2", "amerge=inputs=8", "[0:a:3]"} {
		if !strings.Contains(g, part) {
			t.Fatalf("missing %q in %q", part, g)
		}
	}
}
