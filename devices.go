package main

import (
	"os"
	"os/exec"
	"strings"
)

// getParentDevice returns the base disk name for a partition.
// For example, "nvme0n1p2" becomes "nvme0n1", and "sda1" becomes "sda".
func getParentDevice(dev string) string {
	if strings.HasPrefix(dev, "nvme") {
		if idx := strings.LastIndex(dev, "p"); idx != -1 {
			return dev[:idx]
		}
	}
	// For non-NVMe devices, remove trailing digits.
	i := len(dev) - 1
	for ; i >= 0; i-- {
		if dev[i] < '0' || dev[i] > '9' {
			break
		}
	}
	return dev[:i+1]
}

func getAvailableDevices() ([]string, error) {
	var devices []string

	// Run lsblk without header.
	cmd := exec.Command("lsblk", "-n", "-o", "NAME,MOUNTPOINTS")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	// Build a set of devices that host the root filesystem.
	rootDevices := make(map[string]bool)
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		// Check if the mountpoint is exactly "/".
		if len(fields) >= 2 && fields[1] == "/" {
			// Remove any non-alphanumeric leading characters (e.g. "|-", "`-", etc.)
			cleanName := strings.TrimLeftFunc(fields[0], func(r rune) bool {
				return !((r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'))
			})
			dev := "/dev/" + cleanName
			rootDevices[dev] = true
			// Mark the underlying disk.
			parent := "/dev/" + getParentDevice(cleanName)
			rootDevices[parent] = true
		}
	}

	// Iterate over /sys/block to list available disks.
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		devicePath := "/dev/" + name

		// Skip loop and ram devices.
		if !strings.HasPrefix(name, "loop") && !strings.HasPrefix(name, "ram") {
			if info, err := os.Stat(devicePath); err == nil && info.Mode()&os.ModeDevice != 0 {
				if _, found := rootDevices[devicePath]; found {
					continue
				}
				devices = append(devices, devicePath)
			}
		}
	}

	return devices, nil
}
