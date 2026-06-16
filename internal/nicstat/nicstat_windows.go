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
  $pp=@(Get-NetAdapterAdvancedProperty -Name $a.Name | Where-Object { $_.DisplayName -match 'Energy.?Efficient|Green Ethernet|EEE|Gigabit Lite|Ultra Low Power|Power Saving|System Idle Power|Auto.?Disable Gigabit|Energy.?Detect|Selective Suspend' } | ForEach-Object { "$($_.DisplayName)=$($_.DisplayValue)" })
  [PSCustomObject]@{
    Name=$a.Name; Description=$a.InterfaceDescription; LinkSpeed=[string]$a.LinkSpeed; Status=[string]$a.Status
    RxErrors=[int64]$s.ReceivedPacketErrors; RxDiscards=[int64]$s.ReceivedDiscardedPackets
    TxErrors=[int64]$s.OutboundPacketErrors; TxDiscards=[int64]$s.OutboundDiscardedPackets
    RxBytes=[int64]$s.ReceivedBytes; TxBytes=[int64]$s.OutboundBytes
    Power=[string]($pp -join '; ')
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
