package ui

import "testing"

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

func TestPaletteIsDark(t *testing.T) {
	if colTextPri.R < 0xC0 || colBg.R > 0x40 {
		t.Fatalf("palette is not dark: text=%v bg=%v", colTextPri, colBg)
	}
}
