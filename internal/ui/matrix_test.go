package ui

import (
	"testing"

	"netlogger/internal/appcore"
)

func TestSevColorBands(t *testing.T) {
	if sevColor(0.05, true) != colGood {
		t.Fatalf("low loss should be good")
	}
	if sevColor(0.5, true) != colWatch {
		t.Fatalf("mid loss should be watch")
	}
	if sevColor(5.0, true) != colBad {
		t.Fatalf("high loss should be bad")
	}
	if sevColor(0, false) != colTextMut {
		t.Fatalf("no-data color should be muted")
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
