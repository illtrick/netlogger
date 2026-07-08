package appcore

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"netlogger/internal/store"
)

// fmtCap renders a per-link cap in Mbit/s as a human unit: whole Mb/s below
// 1 Gbit, trimmed Gb/s above ("200 Mb/s", "1.5 Gb/s"). appcore-local sibling of
// ui.fmtRate so history rows read in the same vocabulary. Pure.
func fmtCap(mbit int) string {
	if mbit >= 1000 {
		g := float64(mbit) / 1000
		return strconv.FormatFloat(g, 'f', -1, 64) + " Gb/s"
	}
	return fmt.Sprintf("%d Mb/s", mbit)
}

// mmssStr formats a duration in seconds as M:SS ("10:00"). Pure.
func mmssStr(sec int) string {
	if sec < 0 {
		sec = 0
	}
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

// RecordStressRun persists the orchestrator's summary of a finished stress run.
func (a *App) RecordStressRun(durS, links, capMbit int, proto, worstHost string, worstAddMs float64, aborts int) {
	label := fmt.Sprintf("%d links · %s cap · %s", links, fmtCap(capMbit), strings.ToUpper(proto))
	detail := fmt.Sprintf("%s · worst +%.0f ms on %s · %d aborts", mmssStr(durS), worstAddMs, worstHost, aborts)
	if worstHost == "" {
		detail = fmt.Sprintf("%s · %d aborts", mmssStr(durS), aborts)
	}
	a.recordTestResult(store.TestResult{
		TSUnixUS: time.Now().UnixMicro(),
		Kind:     "stress",
		Label:    label,
		Detail:   detail,
	})
}
