package audiox

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
	return srcPad + "asplit=4[a12s][a34s][a56s][a78s];" +
		"[a12s]pan=stereo|c0=c0|c1=c1[ap0];" +
		"[a34s]pan=stereo|c0=c2|c1=c3[ap1];" +
		"[a56s]pan=stereo|c0=c4|c1=c5[ap2];" +
		"[a78s]pan=stereo|c0=c6|c1=c7[ap3];"
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
