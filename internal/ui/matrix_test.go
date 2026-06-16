package ui

import (
	"image/color"
	"testing"

	"netlogger/internal/appcore"
)

func TestSevColorBands(t *testing.T) {
	good := sevColor(0.05, true)
	warn := sevColor(0.5, true)
	bad := sevColor(5.0, true)
	none := sevColor(0, false)
	if good == warn || warn == bad || good == bad {
		t.Fatalf("severity colors should differ: %v %v %v", good, warn, bad)
	}
	if none != (color.NRGBA{R: 0x99, G: 0x99, B: 0x99, A: 0xff}) {
		t.Fatalf("no-data color wrong: %v", none)
	}
}

func TestCellLabel(t *testing.T) {
	if got := cellLabel(appcore.MatrixCell{LossPct: 1.5}, true); got != "1.5%" {
		t.Fatalf("label = %q, want 1.5%%", got)
	}
	if got := cellLabel(appcore.MatrixCell{}, false); got != "–" {
		t.Fatalf("no-data label = %q, want dash", got)
	}
}
