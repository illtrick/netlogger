//go:build darwin

package nicstat

import (
	"strings"
	"testing"
)

func TestWifiRadioDetail(t *testing.T) {
	w := wifiRadio{
		Iface: "en0", TxRateMbps: 1921, RSSI: -45, Noise: -91,
		Channel: 40, Band: "5 GHz", WidthMHz: 160, PHY: "802.11ax", Security: "WPA2",
	}
	got := w.detail()
	want := "802.11ax · ch 40 (5 GHz, 160 MHz) · RSSI -45 dBm · noise -91 dBm · WPA2"
	if got != want {
		t.Errorf("detail() = %q, want %q", got, want)
	}

	// Sparse radio state must not render empty separators.
	sparse := wifiRadio{RSSI: -60, Noise: -95}
	if got := sparse.detail(); strings.HasPrefix(got, " ·") || strings.Contains(got, "( ") {
		t.Errorf("sparse detail malformed: %q", got)
	}
}

// TestReadWifiRadioSmoke exercises the real CoreWLAN call. It only asserts
// invariants that hold whenever Wi-Fi is associated; skipped otherwise so CI
// on wired-only or radio-off machines stays green.
func TestReadWifiRadioSmoke(t *testing.T) {
	w, ok := readWifiRadio()
	if !ok {
		t.Skip("Wi-Fi off or not associated")
	}
	if w.Iface == "" {
		t.Error("associated radio has no interface name")
	}
	if w.RSSI >= 0 || w.RSSI < -100 {
		t.Errorf("implausible RSSI %d dBm", w.RSSI)
	}
	if w.TxRateMbps <= 0 {
		t.Errorf("implausible tx rate %v", w.TxRateMbps)
	}
	t.Logf("live radio: %s %s tx=%s", w.Iface, w.detail(), formatMbps(w.TxRateMbps))
}
