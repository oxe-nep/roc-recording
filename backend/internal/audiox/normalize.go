package audiox

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	reAudioStream = regexp.MustCompile(`(?i)Stream\s+#\d+:\d+.*?\bAudio:\s*(.+)`)
	reStreamHead  = regexp.MustCompile(`(?i)Stream\s+#\d+:\d+`)
	reNChannels   = regexp.MustCompile(`(?i)(\d+)\s*channels`)
)

// PanTo8 copies the first n input channels into an 8-channel pad (rest silent).
// Only existing channels are referenced so stereo sources do not fail the graph.
func PanTo8(n int) string {
	if n > Channels {
		n = Channels
	}
	if n < 1 {
		n = 1
	}
	parts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		parts = append(parts, fmt.Sprintf("c%d=c%d", i, i))
	}
	return "pan=8c|" + strings.Join(parts, "|")
}

// LinkPad attaches filters to a labeled pad. FFmpeg wants [a8]aresample=… not [a8],aresample=….
func LinkPad(pad, chain string) string {
	return pad + strings.TrimPrefix(chain, ",")
}

// ParseFfprobeChannelCSV reads ffprobe csv=p=0 channel counts, ignoring banner/warning lines.
func ParseFfprobeChannelCSV(raw string) []int {
	var chs []int
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		nStr := line
		if i := strings.IndexByte(line, ','); i >= 0 {
			nStr = strings.TrimSpace(line[:i])
		}
		if nStr == "" || strings.EqualFold(nStr, "N/A") {
			chs = append(chs, 1)
			continue
		}
		n, err := strconv.Atoi(nStr)
		if err != nil {
			continue
		}
		if n < 1 {
			n = 1
		}
		chs = append(chs, n)
	}
	return chs
}

// ParseAudioStreams returns channel counts per audio stream from FFmpeg -i banner text.
func ParseAudioStreams(banner string) []int {
	raw := strings.Split(banner, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, strings.TrimSpace(strings.TrimSuffix(line, "\r")))
	}

	var out []int
	for i, line := range lines {
		if m := reAudioStream.FindStringSubmatch(line); m != nil {
			desc := m[1]
			if i+1 < len(lines) && !hasLayoutHint(desc) && hasLayoutHint(lines[i+1]) {
				desc = desc + " " + lines[i+1]
			}
			out = append(out, channelsFromDesc(desc))
			continue
		}
		// Wrapped banner: "Stream #0:1(eng):" then next line "Audio: pcm_s24le, …"
		if reStreamHead.MatchString(line) && !strings.Contains(strings.ToLower(line), "audio:") &&
			!strings.Contains(strings.ToLower(line), "video:") &&
			!strings.Contains(strings.ToLower(line), "subtitle:") {
			if i+1 < len(lines) && strings.Contains(strings.ToLower(lines[i+1]), "audio:") {
				out = append(out, channelsFromDesc(lines[i+1]))
			}
		}
	}
	return out
}

func hasLayoutHint(s string) bool {
	l := strings.ToLower(s)
	return reNChannels.MatchString(l) ||
		strings.Contains(l, "mono") ||
		strings.Contains(l, "stereo") ||
		strings.Contains(l, "7.1") ||
		strings.Contains(l, "5.1")
}

func channelsFromDesc(desc string) int {
	s := strings.ToLower(desc)
	if m := reNChannels.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err == nil && n > 0 {
			return n
		}
	}
	switch {
	case strings.Contains(s, "7.1"):
		return 8
	case strings.Contains(s, "5.1"):
		return 6
	case strings.Contains(s, "quad") || strings.Contains(s, "4.0"):
		return 4
	case strings.Contains(s, "2.1"):
		return 3
	case strings.Contains(s, "stereo"):
		return 2
	case strings.Contains(s, "mono"):
		return 1
	default:
		return 2
	}
}

// FileTo8 builds a filter that flattens one or more audio streams into [a8] (8 discrete channels).
// fileIdx is the FFmpeg input index (0 = media file).
func FileTo8(fileIdx int, chs []int) (filter, outPad string) {
	outPad = "[a8]"
	if len(chs) == 0 {
		chs = []int{Channels}
	}
	if len(chs) == 1 {
		n := chs[0]
		if n > Channels {
			n = Channels
		}
		if n < 1 {
			n = 1
		}
		src := fmt.Sprintf("[%d:a]", fileIdx)
		return src + "aresample=48000:async=1:first_pts=0," + PanTo8(n) + outPad, outPad
	}

	var parts []string
	var pads []string
	filled := 0
	for si, ch := range chs {
		if filled >= Channels {
			break
		}
		if ch < 1 {
			ch = 1
		}
		take := ch
		if take > Channels-filled {
			take = Channels - filled
		}
		in := fmt.Sprintf("[%d:a:%d]", fileIdx, si)
		name := fmt.Sprintf("s%d", si)
		rs := "aresample=48000:async=1:first_pts=0"
		if take < ch {
			parts = append(parts, fmt.Sprintf("%s%s,%s[%s]", in, rs, panCopy(take), name))
		} else {
			parts = append(parts, fmt.Sprintf("%s%s[%s]", in, rs, name))
		}
		pads = append(pads, "["+name+"]")
		filled += take
	}
	n := len(pads)
	if n == 0 {
		src := fmt.Sprintf("[%d:a]", fileIdx)
		return src + "aresample=48000:async=1:first_pts=0," + PanTo8(2) + outPad, outPad
	}
	merged := strings.Join(pads, "") + fmt.Sprintf("amerge=inputs=%d", n)
	return strings.Join(parts, ";") + ";" + merged + "," + PanTo8(min(filled, Channels)) + outPad, outPad
}

func panCopy(n int) string {
	if n > Channels {
		n = Channels
	}
	if n < 1 {
		n = 1
	}
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = fmt.Sprintf("c%d=c%d", i, i)
	}
	return fmt.Sprintf("pan=%dc|%s", n, strings.Join(parts, "|"))
}
