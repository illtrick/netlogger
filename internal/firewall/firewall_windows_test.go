//go:build windows

package firewall

import "testing"

func TestAllowProgramBestEffort(t *testing.T) {
	if err := AllowProgram("NetLoggerTestRule"); err != nil {
		t.Fatalf("AllowProgram should be best-effort nil, got %v", err)
	}
}
