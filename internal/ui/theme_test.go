package ui

import "testing"

func TestPaletteIsDark(t *testing.T) {
	if colTextPri.R < 0xC0 || colBg.R > 0x40 {
		t.Fatalf("palette is not dark: text=%v bg=%v", colTextPri, colBg)
	}
}
