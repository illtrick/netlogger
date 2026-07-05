//go:build darwin

package nicstat

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework CoreWLAN -framework Foundation

#import <CoreWLAN/CoreWLAN.h>
#include <string.h>

typedef struct {
	char   name[16]; // BSD interface name (en0)
	double txRate;   // current PHY tx rate, Mbps
	long   rssi;     // dBm
	long   noise;    // dBm
	long   channel;
	long   band;  // CWChannelBand: 1=2.4GHz 2=5GHz 3=6GHz
	long   width; // CWChannelWidth: 1=20 2=40 3=80 4=160 MHz
	long   phy;   // CWPHYMode
	long   sec;   // CWSecurity
	int    ok;
} wifiStat;

// readWifi reads the associated Wi-Fi interface's live radio state. RSSI,
// noise, tx rate, and channel are NOT gated behind the Location permission
// (only SSID/BSSID/scan results are); no root, no entitlement. ~10ms.
static wifiStat readWifi(void) {
	wifiStat s;
	memset(&s, 0, sizeof(s));
	@autoreleasepool {
		CWInterface *ifc = CWWiFiClient.sharedWiFiClient.interface;
		if (ifc == nil || !ifc.powerOn || !ifc.serviceActive) {
			return s;
		}
		const char *n = ifc.interfaceName.UTF8String;
		if (n != NULL) {
			strncpy(s.name, n, sizeof(s.name) - 1);
		}
		s.txRate = ifc.transmitRate;
		s.rssi = ifc.rssiValue;
		s.noise = ifc.noiseMeasurement;
		CWChannel *ch = ifc.wlanChannel;
		if (ch != nil) {
			s.channel = ch.channelNumber;
			s.band = ch.channelBand;
			s.width = ch.channelWidth;
		}
		s.phy = ifc.activePHYMode;
		s.sec = ifc.security;
		s.ok = 1;
	}
	return s;
}
*/
import "C"

import (
	"fmt"
	"strings"
)

// wifiRadio is the associated Wi-Fi interface's live radio state.
type wifiRadio struct {
	Iface      string  // en0
	TxRateMbps float64 // current PHY rate
	RSSI       int     // dBm
	Noise      int     // dBm
	Channel    int
	Band       string // "2.4 GHz" / "5 GHz" / "6 GHz"
	WidthMHz   int
	PHY        string // 802.11n/ac/ax/…
	Security   string // open/WPA2/WPA3/secured
}

// readWifiRadio returns the live radio state, or ok=false when Wi-Fi is off
// or not associated (CoreWLAN degrades cleanly; never fails the poll).
func readWifiRadio() (wifiRadio, bool) {
	s := C.readWifi()
	if s.ok == 0 {
		return wifiRadio{}, false
	}
	bands := map[int]string{1: "2.4 GHz", 2: "5 GHz", 3: "6 GHz"}
	widths := map[int]int{1: 20, 2: 40, 3: 80, 4: 160}
	phys := map[int]string{1: "802.11a", 2: "802.11b", 3: "802.11g", 4: "802.11n", 5: "802.11ac", 6: "802.11ax", 7: "802.11be"}
	secs := map[int]string{0: "open"}
	for _, i := range []int{2, 3, 4, 5} {
		secs[i] = "WPA2" // WPA/WPA2 personal + mixed variants
	}
	secs[11], secs[12] = "WPA3", "WPA3"
	sec := secs[int(s.sec)]
	if sec == "" {
		sec = "secured"
	}
	return wifiRadio{
		Iface:      C.GoString(&s.name[0]),
		TxRateMbps: float64(s.txRate),
		RSSI:       int(s.rssi),
		Noise:      int(s.noise),
		Channel:    int(s.channel),
		Band:       bands[int(s.band)],
		WidthMHz:   widths[int(s.width)],
		PHY:        phys[int(s.phy)],
		Security:   sec,
	}, true
}

// detail renders the radio state for the Adapters panel.
func (w wifiRadio) detail() string {
	var b strings.Builder
	if w.PHY != "" {
		b.WriteString(w.PHY)
	}
	if w.Channel > 0 {
		fmt.Fprintf(&b, " · ch %d", w.Channel)
		if w.Band != "" || w.WidthMHz > 0 {
			b.WriteString(" (")
			b.WriteString(w.Band)
			if w.WidthMHz > 0 {
				fmt.Fprintf(&b, ", %d MHz", w.WidthMHz)
			}
			b.WriteString(")")
		}
	}
	fmt.Fprintf(&b, " · RSSI %d dBm · noise %d dBm", w.RSSI, w.Noise)
	if w.Security != "" {
		b.WriteString(" · " + w.Security)
	}
	return strings.TrimPrefix(b.String(), " · ")
}
