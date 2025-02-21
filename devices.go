package main

import (
	"os"
	"os/exec"
	"strings"
)

// getParentDevice returns the base disk name for a partition.
// For example, "nvme0n1p2" becomes "nvme0n1" and "sda1" becomes "sda".
func getParentDevice(dev string) string {
	if strings.HasPrefix(dev, "nvme") {
		if idx := strings.LastIndex(dev, "p"); idx != -1 {
			return dev[:idx]
		}
	}
	// Remove trailing digits for non-NVMe devices.
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

	// Get lsblk output without header.
	cmd := exec.Command("lsblk", "-n", "-o", "NAME,MOUNTPOINT")
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
		// If the MOUNTPOINT is "/" then this device or its partition hosts root.
		if len(fields) >= 2 && fields[1] == "/" {
			// Remove any tree-drawing characters (like "├─" or "└─") from the device name.
			cleanName := strings.TrimLeft(fields[0], "├─└")
			dev := "/dev/" + cleanName
			rootDevices[dev] = true
			// Also mark its parent disk.
			parent := "/dev/" + getParentDevice(cleanName)
			rootDevices[parent] = true
		}
	}

	// Iterate over /sys/block entries.
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
