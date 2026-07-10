//go:build windows

package firewall

import (
	"strings"
	"testing"
)

func TestPsQuoteEscapesSingleQuotes(t *testing.T) {
	if got := psQuote("O'Brien's rule"); got != "'O''Brien''s rule'" {
		t.Fatalf("psQuote = %q", got)
	}
}

func TestHealthProbeShape(t *testing.T) {
	// Program given: the probe must gate on enabled+allow+inbound AND the path.
	p := healthProbe("NetLogger", `C:\apps\NetLogger.exe`)
	for _, want := range []string{"'NetLogger'", "Enabled", "Allow", "Inbound", `C:\apps\NetLogger.exe`, "Get-NetFirewallApplicationFilter"} {
		if !strings.Contains(p, want) {
			t.Errorf("probe missing %q: %s", want, p)
		}
	}
	// No program: no application-filter clause (port/ICMP rules have no path).
	p = healthProbe("NetLogger ICMP v4", "")
	if strings.Contains(p, "ApplicationFilter") {
		t.Errorf("portless probe should not check program: %s", p)
	}
}

func TestRuleHealthyFalseForMissingRule(t *testing.T) {
	if ruleHealthy("NetLogger-does-not-exist-9c1f", "") {
		t.Fatalf("nonexistent rule must not report healthy")
	}
}

func TestAllowProgramBestEffort(t *testing.T) {
	if err := AllowProgram("NetLoggerTestRule"); err != nil {
		t.Fatalf("AllowProgram should be best-effort nil, got %v", err)
	}
}

func TestAllowPingBestEffort(t *testing.T) {
	if err := AllowPing("NetLoggerTestICMP"); err != nil {
		t.Fatalf("AllowPing should be best-effort nil, got %v", err)
	}
}
