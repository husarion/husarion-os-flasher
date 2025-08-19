package util

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// IsRaspberryPi checks if the current device is a Raspberry Pi
func IsRaspberryPi() bool {
	_, err := exec.Command("grep", "-q", "Raspberry Pi", "/proc/cpuinfo").Output()
	return err == nil
}

// GetDiskSize returns the size (in bytes) of a disk using "blockdev --getsize64"
func GetDiskSize(device string) (int64, error) {
	out, err := exec.Command("blockdev", "--getsize64", device).Output()
	if err != nil {
		return 0, err
	}
	sizeStr := strings.TrimSpace(string(out))
	return strconv.ParseInt(sizeStr, 10, 64)
}

// FormatBytes returns a human-friendly string for a byte count
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// FormatDuration formats a duration in a human-readable way using short format
func FormatDuration(d time.Duration) string {
	// Round to seconds
	seconds := int(d.Seconds())
	
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	
	minutes := seconds / 60
	seconds = seconds % 60
	
	if minutes < 60 {
		if seconds == 0 {
			return fmt.Sprintf("%dm", minutes)
		}
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	
	hours := minutes / 60
	minutes = minutes % 60
	
	if minutes == 0 && seconds == 0 {
		return fmt.Sprintf("%dh", hours)
	} else if seconds == 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	
	return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
}
