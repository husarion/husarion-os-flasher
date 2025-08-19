package ui

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"github.com/husarion/husarion-os-flasher/util"

	"github.com/creack/pty"

	tea "github.com/charmbracelet/bubbletea"
)

func GetImageFiles(osImgPath string) ([]string, error) {
	// Use osImgPath instead of hardcoded "/os-images"
	entries, err := os.ReadDir(osImgPath)
	if err != nil {
		return nil, err
	}

	var images []string
	for _, entry := range entries {
		if !entry.IsDir() {
			ext := filepath.Ext(entry.Name())
			name := entry.Name()

			// Support both .img and .img.xz files
			if ext == ".img" || (ext == ".xz" && strings.HasSuffix(name, ".img.xz")) {
				images = append(images, filepath.Join(osImgPath, name))
			}
		}
	}

	return images, nil
}

func WriteImage(src, dst string, progressChan chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		// Unmount all partitions under the selected device (e.g. /dev/sda -> /dev/sda1, /dev/sda2, etc.)
		progressChan <- ProgressMsg("Unmounting all partitions under " + dst + " if mounted...")

		// Check if the device is mounted before attempting to unmount
		checkCmd := exec.Command("sh", "-c", "mount | grep "+dst)
		if err := checkCmd.Run(); err == nil {
			// Device is mounted, proceed to unmount
			if err := exec.Command("sh", "-c", "umount "+dst+"*").Run(); err != nil {
				progressChan <- ProgressMsg("Unmount error (ignored): " + err.Error())
			}
		} else {
			progressChan <- ProgressMsg("No partitions to unmount under " + dst)
		}

		// Determine if we're dealing with a compressed image
		isCompressed := strings.HasSuffix(src, ".img.xz")

		var cmd *exec.Cmd
		if isCompressed {
			// For compressed .img.xz files, check if xz is available
			_, err := exec.LookPath("xz")
			if err != nil {
				progressChan <- ErrorMsg{Err: fmt.Errorf("cannot decompress .xz file: xz utility not found")}
				return nil
			}

			progressChan <- ProgressMsg("Preparing to flash compressed image...")

			// Get the uncompressed size using xz -l
			sizeCmd := exec.Command("xz", "-l", src)
			output, err := sizeCmd.Output()

			var uncompressedSizeBytes int64
			if err == nil {
				// Parse the tabular output from xz -l
				lines := strings.Split(string(output), "\n")
				if len(lines) >= 2 { // Need at least header and data line
					dataLine := lines[1] // Second line contains the data
					fields := strings.Fields(dataLine)

					// Find the uncompressed size (column 4) and unit (part of the same value or next field)
					if len(fields) >= 5 {
						sizeStr := fields[3] // Uncompressed size value
						unitStr := fields[4] // Unit (GiB, MiB, etc.)

						// Parse the size, removing commas if present
						sizeValue, err := strconv.ParseFloat(strings.ReplaceAll(sizeStr, ",", ""), 64)
						if err == nil && unitStr == "GiB" {
							// Convert GiB to bytes
							uncompressedSizeBytes = int64(sizeValue * 1024 * 1024 * 1024)
						} else if err == nil && unitStr == "MiB" {
							// Convert MiB to bytes
							uncompressedSizeBytes = int64(sizeValue * 1024 * 1024)
						}
					}
				}
			}

			// If we couldn't get the size from xz -l, estimate it from the compressed size
			if uncompressedSizeBytes == 0 {
				fileInfo, err := os.Stat(src)
				if err == nil {
					// Use a 5x multiplier as a reasonable estimate for disk images
					uncompressedSizeBytes = fileInfo.Size() * 5
					progressChan <- ProgressMsg("Using estimated uncompressed size for progress tracking")
				}
			}

			if uncompressedSizeBytes > 0 {
				// Use pv with size parameter for accurate progress reporting
				progressChan <- ProgressMsg(fmt.Sprintf("Decompressing and flashing (size: %s)...",
					util.FormatBytes(uncompressedSizeBytes)))

				// Use bash explicitly instead of sh for pipefail support
				cmd = exec.Command("bash", "-c", fmt.Sprintf("set -o pipefail; xz -dc %s 2>/tmp/xz_error | pv -s %d | dd of=%s bs=1k",
					src, uncompressedSizeBytes, dst))
			} else {
				// Fallback if we couldn't determine the size
				progressChan <- ProgressMsg("Decompressing and flashing (no size info)...")
				// Use bash explicitly instead of sh for pipefail support
				cmd = exec.Command("bash", "-c", fmt.Sprintf("set -o pipefail; xz -dc %s 2>/tmp/xz_error | pv | dd of=%s bs=1k",
					src, dst))
			}
		} else {
			// Standard uncompressed image - also switch to bash for consistency
			cmd = exec.Command("bash", "-c", fmt.Sprintf("pv %s | dd of=%s bs=1k", src, dst))
		}
		ptmx, err := pty.Start(cmd)
		if err != nil {
			progressChan <- ErrorMsg{Err: fmt.Errorf("failed to start dd command: %v", err)}
			return nil
		}

		// Send DDStartedMsg so the model stores the dd command pointer for aborting.
		progressChan <- DDStartedMsg{Cmd: cmd}

		go func() {
			scanner := bufio.NewScanner(ptmx)
			// Custom split function: split on carriage return OR newline.
			scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
				if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
					return i + 1, data[:i], nil
				}
				if atEOF && len(data) > 0 {
					return len(data), data, nil
				}
				return 0, nil, nil
			})

			for scanner.Scan() {
				line := scanner.Text()
				trimmed := strings.TrimSpace(line)
				if len(trimmed) > 0 {
					progressChan <- ProgressMsg(trimmed)
				}
			}

			if err := cmd.Wait(); err != nil {
				// Check if the error might be due to xz corruption
				if isCompressed {
					// Try to read any error output from xz
					if xzErrorData, readErr := os.ReadFile("/tmp/xz_error"); readErr == nil && len(xzErrorData) > 0 {
						progressChan <- ErrorMsg{Err: fmt.Errorf("compressed file error: %s", string(xzErrorData))}
					} else {
						progressChan <- ErrorMsg{Err: fmt.Errorf("decompression or dd command failed: %v", err)}
					}
				} else {
					progressChan <- ErrorMsg{Err: fmt.Errorf("dd command failed: %v", err)}
				}
			} else {
				progressChan <- ProgressMsg("Syncing...")
				if err := exec.Command("sync").Run(); err != nil {
					progressChan <- ErrorMsg{Err: fmt.Errorf("sync failed: %v", err)}
				} else {
					progressChan <- ProgressMsg("Sync completed successfully.")
					// Include source and destination in the done message
					progressChan <- DoneMsg{Src: src, Dst: dst}
				}
			}
		}()

		return nil
	}
}

