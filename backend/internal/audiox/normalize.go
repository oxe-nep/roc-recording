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
//
// 4× stereo cannot be amerge'd as stereo (layout overlap → "Error reinitializing filters").
// Split each pair to mono, amerge 8 lanes, then pan=8c. Never 7.1 — that layout is unstable on DeckLink.
func FileTo8(fileIdx int, chs []int) (filter, outPad string) {
	outPad = "[a8]"
	if len(chs) == 0 {
		chs = []int{Channels}
	}
	rs := "aresample=48000:async=1:first_pts=0"
	if len(chs) == 1 {
		n := chs[0]
		if n > Channels {
			n = Channels
		}
		if n < 1 {
			n = 1
		}
		src := fmt.Sprintf("[%d:a]", fileIdx)
		return src + rs + "," + PanTo8(n) + outPad, outPad
	}

	var parts []string
	var lanes []string
	lane := 0
	for si, ch := range chs {
		if lane >= Channels {
			break
		}
		in := fmt.Sprintf("[%d:a:%d]", fileIdx, si)
		if ch < 1 {
			ch = 1
		}
		if ch == 1 {
			pad := fmt.Sprintf("ln%d", lane)
			parts = append(parts, in+rs+",aformat=channel_layouts=mono["+pad+"]")
			lanes = append(lanes, "["+pad+"]")
			lane++
			continue
		}
		take := ch
		if take > Channels-lane {
			take = Channels - lane
		}
		splitName := fmt.Sprintf("sp%d", si)
		parts = append(parts, fmt.Sprintf("%s%s,asplit=%d", in, rs, take)+splitPads(splitName, take))
		for c := 0; c < take; c++ {
			pad := fmt.Sprintf("ln%d", lane)
			parts = append(parts, fmt.Sprintf("[%s%d]pan=mono|c0=c%d[%s]", splitName, c, c, pad))
			lanes = append(lanes, "["+pad+"]")
			lane++
		}
	}
	n := len(lanes)
	if n == 0 {
		src := fmt.Sprintf("[%d:a]", fileIdx)
		return src + rs + "," + PanTo8(2) + outPad, outPad
	}
	merged := strings.Join(lanes, "") + fmt.Sprintf("amerge=inputs=%d", n)
	return strings.Join(parts, ";") + ";" + merged + "," + PanTo8(min(n, Channels)) + outPad, outPad
}

func splitPads(prefix string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte('[')
		b.WriteString(prefix)
		b.WriteString(strconv.Itoa(i))
		b.WriteByte(']')
	}
	return b.String()
}
