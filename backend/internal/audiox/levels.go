package audiox

import "fmt"

const (
	Channels = 8
	Silence  = -90.0
)

// Discrete8Pan maps input channels 1–8 1:1 (missing inputs become silent).
const Discrete8Pan = "pan=8c|c0=c0|c1=c1|c2=c2|c3=c3|c4=c4|c5=c5|c6=c6|c7=c7"

func NormalizeCount(n int) int {
	if n == Channels {
		return Channels
	}
	return 2
}

func SilencePeaks() [Channels]float64 {
	var p [Channels]float64
	for i := range p {
		p[i] = Silence
	}
	return p
}

func SetPeak(p *[Channels]float64, ch int, val float64) {
	if ch >= 1 && ch <= Channels {
		p[ch-1] = val
	}
}

func Slice(p [Channels]float64) []float64 {
	out := make([]float64, Channels)
	copy(out, p[:])
	return out
}

// PreviewPairGraph splits an 8-channel pad into four stereo preview pads [ap0]..[ap3].
func PreviewPairGraph(srcPad string) string {
	return PairSplitGraph(srcPad, "pv", "ap")
}

// PairSplitGraph splits an 8-channel pad into four stereo pads [outPrefix0]..[outPrefix3].
// tag must be unique within the same filter_complex (internal split labels).
func PairSplitGraph(srcPad, tag, outPrefix string) string {
	s0, s1, s2, s3 := tag+"12", tag+"34", tag+"56", tag+"78"
	o0, o1, o2, o3 := outPrefix+"0", outPrefix+"1", outPrefix+"2", outPrefix+"3"
	return srcPad + fmt.Sprintf("asplit=4[%s][%s][%s][%s];", s0, s1, s2, s3) +
		fmt.Sprintf("[%s]pan=stereo|c0=c0|c1=c1[%s];", s0, o0) +
		fmt.Sprintf("[%s]pan=stereo|c0=c2|c1=c3[%s];", s1, o1) +
		fmt.Sprintf("[%s]pan=stereo|c0=c4|c1=c5[%s];", s2, o2) +
		fmt.Sprintf("[%s]pan=stereo|c0=c6|c1=c7[%s];", s3, o3)
}

func PreviewPairMaps() []string {
	return []string{"[ap0]", "[ap1]", "[ap2]", "[ap3]"}
}

func PreviewPairTitle(i int) string {
	labels := []string{"1-2", "3-4", "5-6", "7-8"}
	if i < 0 || i >= len(labels) {
		return "1-2"
	}
	return labels[i]
}
