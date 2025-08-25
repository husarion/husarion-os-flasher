package ui

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
)

// GetParentDevice returns the base disk name for a partition.
// For example, "nvme0n1p2" becomes "nvme0n1", and "sda1" becomes "sda".
func GetParentDevice(dev string) string {
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

// FindmntOutput represents the JSON structure of findmnt --json output
type FindmntOutput struct {
	Filesystems []struct {
		Source string `json:"source"`
	} `json:"filesystems"`
}

// LsblkOutput represents the JSON structure of lsblk --json output
type LsblkOutput struct {
	Blockdevices []struct {
		Name        string   `json:"name"`
		Mountpoints []string `json:"mountpoints"`
		Children    []struct {
			Name        string   `json:"name"`
			Mountpoints []string `json:"mountpoints"`
		} `json:"children,omitempty"`
	} `json:"blockdevices"`
}

func GetAvailableDevices() ([]string, error) {
	var devices []string
	rootDeviceNames := make(map[string]bool)

	// Use findmnt with JSON output to identify the root filesystem device
	rootCmd := exec.Command("findmnt", "--json", "-o", "SOURCE", "/")
	rootOutput, err := rootCmd.Output()
	if err == nil {
		var findmntData FindmntOutput
		if err := json.Unmarshal(rootOutput, &findmntData); err == nil && len(findmntData.Filesystems) > 0 {
			rootDevice := findmntData.Filesystems[0].Source
			// Remove /dev/ prefix if present
			rootDevice = strings.TrimPrefix(rootDevice, "/dev/")
			// Mark both the partition and its parent device as root devices
			rootDeviceNames[rootDevice] = true
			rootDeviceNames[GetParentDevice(rootDevice)] = true
		}
	}

	// Use lsblk with JSON output to get detailed information about all block devices
	cmd := exec.Command("lsblk", "--json", "-o", "NAME,MOUNTPOINTS")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	// Parse the JSON output
	var lsblkData LsblkOutput
	if err := json.Unmarshal(output, &lsblkData); err != nil {
		return nil, err
	}

	// Process devices and find those containing root mountpoint
	for _, device := range lsblkData.Blockdevices {
		// Check if this device has the root mountpoint
		for _, mount := range device.Mountpoints {
			if mount == "/" {
				rootDeviceNames[device.Name] = true
				rootDeviceNames[GetParentDevice(device.Name)] = true
			}
		}

		// Also check children (partitions)
		for _, child := range device.Children {
			for _, mount := range child.Mountpoints {
				if mount == "/" {
					rootDeviceNames[child.Name] = true
					rootDeviceNames[device.Name] = true // Parent device
				}
			}
		}
	}

	// Iterate over /sys/block to list available disks
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		name := entry.Name()
		devicePath := "/dev/" + name

		// Skip loop and ram devices.
		if !strings.HasPrefix(name, "loop") && !strings.HasPrefix(name, "ram") {
			// Skip if this device is a root device or its partition is a root device
			if rootDeviceNames[name] {
				continue
			}
			if info, err := os.Stat(devicePath); err == nil && info.Mode()&os.ModeDevice != 0 {
				devices = append(devices, devicePath)
			}
		}
	}

	return devices, nil
}
