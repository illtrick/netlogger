//go:build windows

package nicstat

import (
	"os/exec"
	"syscall"
)

const psScript = `$ErrorActionPreference='SilentlyContinue'
@(Get-NetAdapter -Physical | ForEach-Object {
  $a=$_
  $s=Get-NetAdapterStatistics -Name $a.Name
  $eee=(Get-NetAdapterAdvancedProperty -Name $a.Name | Where-Object { $_.DisplayName -match 'Energy.?Efficient|Green Ethernet|EEE|Gigabit Lite' } | Select-Object -First 1).DisplayValue
  [PSCustomObject]@{
    Name=$a.Name; Description=$a.InterfaceDescription; LinkSpeed=[string]$a.LinkSpeed; Status=[string]$a.Status
    RxErrors=[int64]$s.ReceivedPacketErrors; RxDiscards=[int64]$s.ReceivedDiscardedPackets
    TxErrors=[int64]$s.OutboundPacketErrors; TxDiscards=[int64]$s.OutboundDiscardedPackets
    RxBytes=[int64]$s.ReceivedBytes; TxBytes=[int64]$s.OutboundBytes
    EEE=[string]$eee
  }
}) | ConvertTo-Json -Compress`

// Collect runs the PowerShell probe and returns parsed adapter stats (nil on error).
func Collect() []NIC {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil
	}
	nics, _ := parseNICs(out)
	return nics
}
