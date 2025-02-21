package main

import (
	"os"
	"strings"
)

func getAvailableDevices() ([]string, error) {
	var devices []string

	rootMount, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil, err
	}
	rootDev := ""
	for _, line := range strings.Split(string(rootMount), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "/" {
			rootDev = fields[0]
			break
		}
	}

	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		name := entry.Name()
		devicePath := "/dev/" + name

		// Skip loop devices and verify it’s a block device.
		if !strings.HasPrefix(name, "loop") {
			if info, err := os.Stat(devicePath); err == nil && info.Mode()&os.ModeDevice != 0 {
				// Ensure this device (or its partitions) is not part of the root filesystem.
				if !strings.HasPrefix(rootDev, devicePath) {
					devices = append(devices, devicePath)
				}
			}
		}
	}

	return devices, nil
}
