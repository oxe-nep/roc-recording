package audiox

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	reAudioStream = regexp.MustCompile(`(?i)Stream\s+#\d+:\d+.*?\bAudio:\s*(.+)`)
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

// ParseAudioStreams returns channel counts per audio stream from FFmpeg -i banner text.
func ParseAudioStreams(banner string) []int {
	var out []int
	for _, line := range strings.Split(banner, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		m := reAudioStream.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out = append(out, channelsFromDesc(m[1]))
	}
	if len(out) == 0 {
		for _, line := range strings.Split(banner, "\n") {
			l := strings.ToLower(line)
			if strings.Contains(l, "stream #") && strings.Contains(l, "audio:") {
				out = append(out, channelsFromDesc(line))
			}
		}
	}
	return out
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
		src := fmt.Sprintf("[%d:a]", fileIdx)
		n := chs[0]
		if n > Channels {
			n = Channels
		}
		if n < 1 {
			n = 1
		}
		return src + "aresample=48000," + PanTo8(n) + outPad, outPad
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
			parts = append(parts, in+"aresample=48000,aformat=channel_layouts=mono["+pad+"]")
			lanes = append(lanes, "["+pad+"]")
			lane++
			continue
		}
		take := ch
		if take > Channels-lane {
			take = Channels - lane
		}
		splitName := fmt.Sprintf("sp%d", si)
		parts = append(parts, fmt.Sprintf("%saresample=48000,asplit=%d", in, take)+splitPads(splitName, take))
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
		return src + "aresample=48000," + PanTo8(2) + outPad, outPad
	}
	merged := strings.Join(lanes, "") + fmt.Sprintf("amerge=inputs=%d", n)
	if n >= Channels {
		return strings.Join(parts, ";") + ";" + merged + ",aformat=channel_layouts=7.1" + outPad, outPad
	}
	return strings.Join(parts, ";") + ";" + merged + "," + PanTo8(n) + outPad, outPad
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
