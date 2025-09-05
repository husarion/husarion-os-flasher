package ui

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"github.com/husarion/husarion-os-flasher/util"

	"github.com/creack/pty"

	tea "github.com/charmbracelet/bubbletea"
)

// --- added helpers (no xz --robot; parse human xz -l output) ---
// parseHumanSize converts "<num>[.<num>] <UNIT>" (with optional commas) to bytes.
func parseHumanSize(num, unit string) (int64, bool) {
	num = strings.ReplaceAll(num, ",", "")
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, false
	}
	multipliers := map[string]float64{
		"B": 1,
		"KiB": 1024,
		"MiB": 1024 * 1024,
		"GiB": 1024 * 1024 * 1024,
		"TiB": 1024 * 1024 * 1024 * 1024,
	}
	unit = strings.TrimSpace(unit)
	m, ok := multipliers[unit]
	if !ok {
		// Sometimes xz prints just "B" or already suffixed like "1234B"
		if strings.HasSuffix(num, "B") {
			trim := strings.TrimSuffix(num, "B")
			f2, err2 := strconv.ParseFloat(trim, 64)
			if err2 == nil {
				return int64(f2), true
			}
		}
		return 0, false
	}
	return int64(f * m), true
}

// getUncompressedSizeFromXZ runs `xz -l` and extracts the uncompressed size.
// Returns (bytes, exact).
func getUncompressedSizeFromXZ(path string) (int64, bool) {
	out, err := exec.Command("xz", "-l", path).CombinedOutput()
	if err != nil {
		return 0, false
	}
	lines := strings.Split(string(out), "\n")
	filename := filepath.Base(path)
	sizeRe := regexp.MustCompile(`([0-9][0-9,]*\.?[0-9]*)\s*(B|KiB|MiB|GiB|TiB)`)
	for _, line := range lines {
		if !strings.Contains(line, filename) {
			continue
		}
		// Find all size occurrences (compressed, uncompressed, maybe more)
		matches := sizeRe.FindAllStringSubmatch(line, -1)
		if len(matches) >= 2 {
			// Second match is uncompressed.
			if val, ok := parseHumanSize(matches[1][1], matches[1][2]); ok {
				return val, true
			}
		}
	}
	// Fallback: try last non-empty numeric line (totals)
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		matches := sizeRe.FindAllStringSubmatch(line, -1)
		if len(matches) >= 2 {
			if val, ok := parseHumanSize(matches[1][1], matches[1][2]); ok {
				return val, true
			}
		}
	}
	return 0, false
}
// --- end helpers ---

func GetImageFiles(osImgPath string) ([]string, error) {
	// Use osImgPath instead of hardcoded "/os-images"
	entries, err := os.ReadDir(osImgPath)
	if err != nil {
		return nil, err
	}

	var images []string
	for _, entry := range entries {
		// Skip directories and macOS metadata items
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "._") {
			continue
		}

		ext := filepath.Ext(name)

		// Support both .img and .img.xz files
		if ext == ".img" || (ext == ".xz" && strings.HasSuffix(name, ".img.xz")) {
			images = append(images, filepath.Join(osImgPath, name))
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

			// Replace previous --robot parsing: use human output only
			uncompressedSizeBytes, exact := getUncompressedSizeFromXZ(src)
			if !exact {
				// Fallback: estimate from compressed size
				if fi, fe := os.Stat(src); fe == nil {
					uncompressedSizeBytes = fi.Size() * 4 // heuristic
					progressChan <- ProgressMsg("Uncompressed size estimated (xz -l parse failed)")
				} else {
					progressChan <- ProgressMsg("Unable to stat file for size estimation; progress will be free-running")
				}
			} else {
				progressChan <- ProgressMsg("Uncompressed size detected: " + util.FormatBytes(uncompressedSizeBytes))
			}

			if uncompressedSizeBytes > 0 {
				tag := "size (exact)"
				if !exact {
					tag = "size (estimated)"
				}
				progressChan <- ProgressMsg(fmt.Sprintf("Decompressing and flashing (%s: %s)...",
					tag, util.FormatBytes(uncompressedSizeBytes)))

				cmd = exec.Command("bash", "-c",
					fmt.Sprintf("set -o pipefail; xz -dc %q 2>/tmp/xz_error | pv -f -s %d | dd of=%q bs=16M oflag=direct status=none",
						src, uncompressedSizeBytes, dst))
			} else {
				progressChan <- ProgressMsg("Decompressing and flashing (no size info)...")
				cmd = exec.Command("bash", "-c",
					fmt.Sprintf("set -o pipefail; xz -dc %q 2>/tmp/xz_error | pv -f | dd of=%q bs=16M oflag=direct status=none",
						src, dst))
			}
		} else {
			// Standard uncompressed image
			cmd = exec.Command("bash", "-c",
				fmt.Sprintf("pv -f %q | dd of=%q bs=16M oflag=direct status=none", src, dst))
		}
		ptmx, err := pty.Start(cmd)
		if err != nil {
			progressChan <- ErrorMsg{Err: fmt.Errorf("failed to start dd command: %v", err)}
			return nil
		}

		// Send DDStartedMsg so the model stores the dd command pointer for aborting.
		progressChan <- DDStartedMsg{Cmd: cmd, Pty: ptmx}

		go func() {
			defer ptmx.Close() // Ensure pty is closed when goroutine exits
			
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

			// Use a channel to monitor process completion with timeout
			done := make(chan error, 1)
			go func() {
				done <- cmd.Wait()
			}()

			// Track last progress to detect hangs
			lastProgressTime := time.Now()
			progressTimeout := 120 * time.Second // 120 seconds without progress = timeout

			for {
				select {
				case err := <-done:
					// Process completed normally, handle the result
					if err != nil {
						// Check if the error might be due to xz corruption
						var errMsg error
						if isCompressed {
							// Try to read any error output from xz
							if xzErrorData, readErr := os.ReadFile("/tmp/xz_error"); readErr == nil && len(xzErrorData) > 0 {
								errMsg = fmt.Errorf("compressed file error: %s", string(xzErrorData))
							} else {
								errMsg = fmt.Errorf("decompression or dd command failed: %v", err)
							}
						} else {
							errMsg = fmt.Errorf("dd command failed: %v", err)
						}
						
						// Safe send to progress channel
						select {
						case progressChan <- ErrorMsg{Err: errMsg}:
						default:
							return
						}
					} else {
						select {
						case progressChan <- ProgressMsg("Syncing..."):
						default:
							return
						}
						
						if err := exec.Command("sync").Run(); err != nil {
							select {
							case progressChan <- ErrorMsg{Err: fmt.Errorf("sync failed: %v", err)}:
							default:
								return
							}
						} else {
							select {
							case progressChan <- ProgressMsg("Sync completed successfully."):
							default:
								return
							}
							
							// Include source and destination in the done message
							select {
							case progressChan <- DoneMsg{Src: src, Dst: dst}:
							default:
								return
							}
						}
					}
					return

				case <-time.After(1 * time.Second):
					// Check for new progress every second
					if scanner.Scan() {
						line := scanner.Text()
						trimmed := strings.TrimSpace(line)
						if len(trimmed) > 0 {
							lastProgressTime = time.Now() // Reset timeout
							// Safe send to progress channel
							select {
							case progressChan <- ProgressMsg(trimmed):
							default:
								// Channel might be closed, exit gracefully
								return
							}
						}
					} else {
						// Scanner finished, check for timeout
						if time.Since(lastProgressTime) > progressTimeout {
							// No progress for too long, likely hung
							select {
							case progressChan <- ErrorMsg{Err: fmt.Errorf("operation timed out - no progress for %v", progressTimeout)}:
							default:
								return
							}
							return
						}
					}
				}
			}
		}()

		return nil
	}
}

