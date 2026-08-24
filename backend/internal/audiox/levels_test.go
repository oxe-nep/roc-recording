package audiox

import (
	"strings"
	"testing"
)

func TestPairSplitGraphUniqueLabels(t *testing.T) {
	a := PairSplitGraph("[aencsrc]", "en", "arec")
	b := PreviewPairGraph("[aprevsrc]")
	if !strings.Contains(a, "[arec0]") || !strings.Contains(a, "[arec3]") {
		t.Fatalf("encode pads: %s", a)
	}
	if !strings.Contains(b, "[ap0]") || !strings.Contains(b, "[ap3]") {
		t.Fatalf("preview pads: %s", b)
	}
	if strings.Contains(a, "[ap0]") || strings.Contains(b, "[arec0]") {
		t.Fatal("encode and preview graphs must not share pad names")
	}
}
