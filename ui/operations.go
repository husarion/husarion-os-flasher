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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/creack/pty"
	"github.com/husarion/husarion-os-flasher/util"
	"gopkg.in/yaml.v3"
)

// StartFlashing initiates the flashing process
func (m *Model) StartFlashing() (tea.Model, tea.Cmd) {
	if m.DeviceList.SelectedItem() == nil || m.ImageList.SelectedItem() == nil || m.Flashing {
		return m, nil
	}

	imagePath := m.ImageList.SelectedItem().(Item).value
	devicePath := m.DeviceList.SelectedItem().(Item).value

	// Create a new buffered progress channel for this run
	m.ProgressChan = make(chan tea.Msg, 100)
	m.Flashing = true
	m.FlashStartTime = time.Now() // Record the start time
	m.Logs = nil
	m.AddLog(fmt.Sprintf("> Starting to flash %s to %s...", imagePath, devicePath))

	// Set focus directly to the Abort button based on system type and layout
	hasCompressedImage := m.IsCompressedImageSelected()
	if util.IsRaspberryPi() {
		if hasCompressedImage {
			m.ActiveList = 6
		} else {
			m.ActiveList = 5
		}
	} else {
		if hasCompressedImage {
			m.ActiveList = 5
		} else {
			m.ActiveList = 4
		}
	}

	return m, tea.Batch(
		WriteImage(imagePath, devicePath, m.ProgressChan),
		ListenProgress(m.ProgressChan),
	)
}

// ConfigEEPROM initiates the EEPROM configuration process
func (m *Model) ConfigEEPROM() (tea.Model, tea.Cmd) {
	if m.ConfiguringEeprom {
		return m, nil
	}

	m.AddLog("> Starting EEPROM configuration...")
	m.ConfiguringEeprom = true

	// Create a function to run the EEPROM configuration command and capture its output
	return m, func() tea.Msg {
		// Replace this with actual EEPROM configuration command
		cmd := exec.Command("rpi-eeprom-config", "--apply", "/etc/boot.conf")

		output, err := cmd.CombinedOutput()
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("error configuring EEPROM: %w", err)}
		}

		// Process the output and return it as a message
		lines := strings.Split(string(output), "\n")
		return EEPROMConfigMsg{Output: lines}
	}
}

// AbortOperation aborts the current operation (flashing or extraction)
func (m *Model) AbortOperation() (tea.Model, tea.Cmd) {
	// Log the abort attempt for debugging
	m.AddLog("> Attempting to abort operation...")
	
	// Check if we're flashing and have a command to abort
	if m.Flashing && m.DdCmd != nil {
		m.Aborting = true
		m.AddLog("Aborting flashing process... (please wait)")

		return m, tea.Sequence(
			tea.Tick(10*time.Millisecond, func(time.Time) tea.Msg { 
				return nil 
			}),
			tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
				err := m.DdCmd.Process.Kill()
				if err != nil {
					return ErrorMsg{Err: fmt.Errorf("error aborting flash: %v", err)}
				}
				// Close the pty to ensure proper cleanup
				if m.DdPty != nil {
					m.DdPty.Close()
				}
				// Don't close the progress channel here - let the goroutine handle it
				return AbortCompletedMsg{}
			}),
		)
	}
	
	// Check if we're extracting and have a command to abort
	if m.Extracting && m.ExtractCmd != nil {
		m.Aborting = true
		m.AddLog("Aborting extraction process... (please wait)")

		return m, tea.Sequence(
			tea.Tick(10*time.Millisecond, func(time.Time) tea.Msg { return nil }),
			tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
				// Kill the process
				if err := m.ExtractCmd.Process.Kill(); err != nil {
					return ErrorMsg{Err: fmt.Errorf("error aborting extraction: %v", err)}
				}
				if m.ExtractPty != nil { _ = m.ExtractPty.Close() }

				// Remove temp and partial files
				if m.ExtractTempPath != "" { _ = os.Remove(m.ExtractTempPath) }
				if m.ExtractOutputPath != "" { _ = os.Remove(m.ExtractOutputPath) }

				return AbortCompletedMsg{}
			}),
		)
	}

	// Check if we're checking integrity and have a command to abort
	if m.Checking && m.CheckCmd != nil {
		m.Aborting = true
		m.AddLog("Aborting integrity check... (please wait)")

		return m, tea.Sequence(
			tea.Tick(10*time.Millisecond, func(time.Time) tea.Msg { return nil }),
			tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
				if err := m.CheckCmd.Process.Kill(); err != nil {
					return ErrorMsg{Err: fmt.Errorf("error aborting check: %v", err)}
				}
				if m.CheckPty != nil { _ = m.CheckPty.Close() }
				return AbortCompletedMsg{}
			}),
		)
	}
	
	m.AddLog("No operation to abort.")
	return m, nil
}

// ExtractWithProgress performs extraction with progress reporting using pv
func ExtractWithProgress(compressedPath, outputPath string, progressChan chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		// Send an initial message to ensure the progress listener is active
		progressChan <- ProgressMsg("Preparing extraction...")

		// Always write to a temp file to avoid half-baked .img
		tempPath := outputPath + ".part"
		_ = os.Remove(tempPath) // best-effort cleanup from previous runs

		// Get compressed file size for initial info
		fileInfo, err := os.Stat(compressedPath)
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to get file info: %v", err)}
		}
		compressedSize := fileInfo.Size()

		// Get uncompressed size using xz -l for accurate progress
		sizeCmd := exec.Command("xz", "-l", compressedPath)
		sizeOutput, err := sizeCmd.Output()
		
		var uncompressedSize int64
		if err == nil {
			// Parse xz -l output to get uncompressed size
			lines := strings.Split(string(sizeOutput), "\n")
			for _, line := range lines {
				// Look for the data line (contains the filename)
				if strings.Contains(line, filepath.Base(compressedPath)) {
					fields := strings.Fields(line)
					if len(fields) >= 5 {
						// Parse the uncompressed size field (e.g., "14.3" + "GiB")
						sizeStr := strings.ReplaceAll(fields[4], ",", "") // Remove commas
						unitStr := fields[5] // Unit
						
						if sizeValue, parseErr := strconv.ParseFloat(sizeStr, 64); parseErr == nil {
							if unitStr == "GiB" {
								uncompressedSize = int64(sizeValue * 1024 * 1024 * 1024)
							} else if unitStr == "MiB" {
								uncompressedSize = int64(sizeValue * 1024 * 1024)
							} else if unitStr == "KiB" {
								uncompressedSize = int64(sizeValue * 1024)
							} else if unitStr == "B" {
								uncompressedSize = int64(sizeValue)
							}
						}
						break
					}
				}
			}
		}

		// Fallback: estimate uncompressed size as 3-5x compressed size
		if uncompressedSize == 0 {
			uncompressedSize = compressedSize * 4
			progressChan <- ProgressMsg("Using estimated uncompressed size for progress")
		}

		// Show initial size information
		progressChan <- ProgressMsg(fmt.Sprintf("Compressed: %s → Estimated uncompressed: %s", 
			util.FormatBytes(compressedSize), util.FormatBytes(uncompressedSize)))

		// Use the same pattern as flashing: xz to decompress and pv to show progress
		// Key fix: write to temp file and rename on success
		var cmd *exec.Cmd
		if uncompressedSize > 0 {
			progressChan <- ProgressMsg(fmt.Sprintf("Extracting (size: %s) → %s", util.FormatBytes(uncompressedSize), filepath.Base(tempPath)))
			cmd = exec.Command("bash", "-c", fmt.Sprintf("set -o pipefail; xz -dc '%s' | pv -f -s %d | dd of='%s' bs=16M", 
				compressedPath, uncompressedSize, tempPath))
		} else {
			progressChan <- ProgressMsg("Extracting (no size info)...")
			cmd = exec.Command("bash", "-c", fmt.Sprintf("set -o pipefail; xz -dc '%s' | pv -f | dd of='%s' bs=16M", 
				compressedPath, tempPath))
		}

		// Use pty.Start like flashing does to capture the progress bar
		ptmx, err := pty.Start(cmd)
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to start extraction command: %v", err)}
		}

		// Send ExtractStartedMsg so the model stores the command pointer for aborting
		progressChan <- ExtractStartedMsg{Cmd: cmd, Pty: ptmx}

		// Use the same scanning pattern as flashing
		go func() {
			defer ptmx.Close() // Ensure pty is closed when goroutine exits
			
			scanner := bufio.NewScanner(ptmx)
			// Custom split function: split on carriage return OR newline (same as flashing)
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
					// Safe send to progress channel
					select {
					case progressChan <- ProgressMsg(trimmed):
					default:
						// Channel might be closed, exit gracefully
						return
					}
				}
			}

			if err := cmd.Wait(); err != nil {
				// On failure, ensure temp file is removed
				_ = os.Remove(tempPath)
				// Safe send to progress channel
				select {
				case progressChan <- ErrorMsg{Err: fmt.Errorf("extraction failed: %v", err)}:
				default:
					// Channel might be closed, exit gracefully
					return
				}
			} else {
				// Sync and atomically move temp to final name
				_ = exec.Command("sync").Run()
				if err := os.Rename(tempPath, outputPath); err != nil {
					_ = os.Remove(tempPath)
					// Safe send to progress channel
					select {
					case progressChan <- ErrorMsg{Err: fmt.Errorf("failed to finalize extracted image: %v", err)}:
					default:
						return
					}
					return
				}

				// Get final size and notify
				if finalInfo, err := os.Stat(outputPath); err == nil {
					finalSize := finalInfo.Size()
					select {
					case progressChan <- ProgressMsg(fmt.Sprintf("Extraction complete. Final size: %s", util.FormatBytes(finalSize))):
					default:
						return
					}
				}
				select {
				case progressChan <- ExtractCompletedMsg{Src: compressedPath, Dst: outputPath}:
				default:
					return
				}
			}
		}()

		return nil
	}
}

// UncompressImage extracts a .img.xz file
func (m *Model) UncompressImage() (tea.Model, tea.Cmd) {
	if !m.IsCompressedImageSelected() || m.Extracting {
		return m, nil
	}

	compressedPath := m.ImageList.SelectedItem().(Item).value
	outputPath := strings.TrimSuffix(compressedPath, ".xz")

	// Track paths on the model for abort cleanup
	m.ExtractOutputPath = outputPath
	m.ExtractTempPath = outputPath + ".part"
	_ = os.Remove(m.ExtractTempPath)

	// Check if output file already exists
	if _, err := os.Stat(outputPath); err == nil {
		// File exists, add log entry
		m.AddLog(fmt.Sprintf("> Output file %s already exists. Removing...", filepath.Base(outputPath)))
		// Remove the existing file
		if err := os.Remove(outputPath); err != nil {
			return m, func() tea.Msg {
				return ErrorMsg{Err: fmt.Errorf("failed to remove existing file: %v", err)}
			}
		}
	}

	// Set extraction state immediately
	m.Extracting = true
	m.ExtractStartTime = time.Now() // Record the start time
	m.AddLog(fmt.Sprintf("> Uncompressing %s to %s...", filepath.Base(compressedPath), filepath.Base(outputPath)))

	// Force cleanup of any previous state
	m.ExtractCmd = nil
	m.ExtractPty = nil
	m.Aborting = false  // Clear aborting state
	
	// Create a new buffered progress channel for this operation (like flashing does)
	m.ProgressChan = make(chan tea.Msg, 100)

	// Set focus to the Abort button based on system type
	if util.IsRaspberryPi() {
		m.ActiveList = 6 // Abort button index on Pi
	} else {
		m.ActiveList = 5 // Abort button index on non-Pi
	}

	// Start the extraction with progress reporting
	return m, tea.Batch(
		func() tea.Msg {
			// Send an immediate message to kickstart the progress listener
			m.ProgressChan <- ProgressMsg("Starting extraction...")
			return nil
		},
		ExtractWithProgress(compressedPath, outputPath, m.ProgressChan),
		ListenProgress(m.ProgressChan),
	)
}

// StartIntegrityCheck initializes integrity checking for the selected image
func (m *Model) StartIntegrityCheck() (tea.Model, tea.Cmd) {
	if m.ImageList.SelectedItem() == nil || m.Checking || m.Flashing || m.Extracting {
		return m, nil
	}

	imagePath := m.ImageList.SelectedItem().(Item).value

	// Prepare state
	m.ProgressChan = make(chan tea.Msg, 100)
	m.Checking = true
	m.Aborting = false
	m.AddLog(fmt.Sprintf("> Checking integrity of %s...", filepath.Base(imagePath)))

	// Focus Abort
	if util.IsRaspberryPi() {
		if m.IsCompressedImageSelected() {
			m.ActiveList = 6
		} else {
			m.ActiveList = 5
		}
	} else {
		if m.IsCompressedImageSelected() {
			m.ActiveList = 5
		} else {
			m.ActiveList = 4
		}
	}

	return m, tea.Batch(
		CheckIntegrity(imagePath, m.ProgressChan),
		ListenProgress(m.ProgressChan),
	)
}

// CheckIntegrity streams progress while verifying the selected image
// - For .img.xz: runs `xz -tv <file>` and streams its progress
// - For .img: compares sha256sum of file against `<file>.checksum`; streams pv progress
func CheckIntegrity(imagePath string, progressChan chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		isCompressed := strings.HasSuffix(imagePath, ".img.xz")

		var cmd *exec.Cmd
		var haveExpected bool
		var expectedFromSidecar string
		if isCompressed {
			cmd = exec.Command("bash", "-c", fmt.Sprintf("set -o pipefail; xz -tv '%s'", imagePath))
		} else {
			checksumPath := imagePath + ".checksum"
			if data, err := os.ReadFile(checksumPath); err == nil {
				expectedFromSidecar = strings.TrimSpace(string(data))
				if sp := strings.Fields(expectedFromSidecar); len(sp) > 0 { expectedFromSidecar = sp[0] }
				if matched, _ := regexp.MatchString(`^[0-9a-fA-F]{64}$`, expectedFromSidecar); matched {
					haveExpected = true
				} else {
					progressChan <- ProgressMsg(fmt.Sprintf("Warning: invalid checksum format in %s; will compute actual hash only", filepath.Base(checksumPath)))
				}
			} else {
				progressChan <- ProgressMsg(fmt.Sprintf("No %s found; computing actual SHA-256 only", filepath.Base(checksumPath)))
			}
			cmd = exec.Command("bash", "-c", fmt.Sprintf("set -o pipefail; pv -f '%s' | sha256sum", imagePath))
		}

		ptmx, err := pty.Start(cmd)
		if err != nil { return ErrorMsg{Err: fmt.Errorf("failed to start integrity command: %v", err)} }
		progressChan <- CheckStartedMsg{Cmd: cmd, Pty: ptmx}

		go func() {
			defer ptmx.Close()
			scanner := bufio.NewScanner(ptmx)
			scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
				if i := bytes.IndexAny(data, "\r\n"); i >= 0 { return i + 1, data[:i], nil }
				if atEOF && len(data) > 0 { return len(data), data, nil }
				return 0, nil, nil
			})

			var finalHash string
			hashRe := regexp.MustCompile(`^[0-9a-fA-F]{64}`)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" { continue }
				if !isCompressed && hashRe.MatchString(line) {
					fields := strings.Fields(line)
					if len(fields) > 0 { finalHash = fields[0] }
				}
				select { case progressChan <- ProgressMsg(line): default: return }
			}

			err := cmd.Wait()
			if isCompressed {
				ok := (err == nil)
				if ok {
					// Also compute sha256 for the compressed file to record actual
					finalHash = ""
					select { case progressChan <- ProgressMsg("Integrity OK. Computing SHA-256 of compressed file..."): default: }
					hashCmd := exec.Command("bash", "-c", fmt.Sprintf("set -o pipefail; pv -f '%s' | sha256sum", imagePath))
					hashPty, herr := pty.Start(hashCmd)
					if herr != nil {
						// Save ok status without actual if hashing can't start
						_ = saveIntegrityResult(imagePath, IntegrityEntry{ Type: "compressed", Method: "xz -tv", Status: "ok", CheckedAt: time.Now().Format(time.RFC3339) })
						select { case progressChan <- ErrorMsg{Err: fmt.Errorf("failed to start sha256sum: %v", herr)}: default: }
						select { case progressChan <- CheckCompletedMsg{File: imagePath, Ok: true}: default: }
						return
					}
					// Announce new step so Abort can target the right process
					progressChan <- CheckStartedMsg{Cmd: hashCmd, Pty: hashPty}

					// Scan hash progress and capture final hash
					hScanner := bufio.NewScanner(hashPty)
					hScanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
						if i := bytes.IndexAny(data, "\r\n"); i >= 0 { return i + 1, data[:i], nil }
						if atEOF && len(data) > 0 { return len(data), data, nil }
						return 0, nil, nil
					})
					for hScanner.Scan() {
						line := strings.TrimSpace(hScanner.Text())
						if line == "" { continue }
						if hashRe.MatchString(line) {
							fields := strings.Fields(line)
							if len(fields) > 0 { finalHash = fields[0] }
						}
						select { case progressChan <- ProgressMsg(line): default: }
					}
					_ = hashCmd.Wait()
					_ = hashPty.Close()

					// Save ok status with actual hash (if captured)
					if werr := saveIntegrityResult(imagePath, IntegrityEntry{ Type: "compressed", Method: "xz -tv", Status: "ok", CheckedAt: time.Now().Format(time.RFC3339), Actual: finalHash }); werr != nil {
						select { case progressChan <- ErrorMsg{Err: fmt.Errorf("failed to write integrity.yaml: %v", werr)}: default: }
					} else {
						select { case progressChan <- ProgressMsg(fmt.Sprintf("Saved integrity record to %s", filepath.Join(filepath.Dir(imagePath), "integrity.yaml"))): default: }
					}
					select { case progressChan <- CheckCompletedMsg{File: imagePath, Ok: true}: default: }
					return
				}

				// Failed xz -tv: compute sha256sum to capture actual checksum
				select { case progressChan <- ProgressMsg("Integrity failed. Computing SHA-256 of compressed file..."): default: }
				hashCmd := exec.Command("bash", "-c", fmt.Sprintf("set -o pipefail; pv -f '%s' | sha256sum", imagePath))
				hashPty, herr := pty.Start(hashCmd)
				if herr != nil {
					// Couldn't start hashing; still save failed status without actual
					_ = saveIntegrityResult(imagePath, IntegrityEntry{ Type: "compressed", Method: "xz -tv", Status: "failed", CheckedAt: time.Now().Format(time.RFC3339) })
					select { case progressChan <- ErrorMsg{Err: fmt.Errorf("failed to start sha256sum: %v", herr)}: default: }
					select { case progressChan <- CheckCompletedMsg{File: imagePath, Ok: false}: default: }
					return
				}
				// Announce new step so Abort can target the right process
				progressChan <- CheckStartedMsg{Cmd: hashCmd, Pty: hashPty}

				// Scan hash progress and capture final hash
				hScanner := bufio.NewScanner(hashPty)
				hScanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
					if i := bytes.IndexAny(data, "\r\n"); i >= 0 { return i + 1, data[:i], nil }
					if atEOF && len(data) > 0 { return len(data), data, nil }
					return 0, nil, nil
				})
				for hScanner.Scan() {
					line := strings.TrimSpace(hScanner.Text())
					if line == "" { continue }
					if hashRe.MatchString(line) {
						fields := strings.Fields(line)
						if len(fields) > 0 { finalHash = fields[0] }
					}
					select { case progressChan <- ProgressMsg(line): default: }
				}
				_ = hashCmd.Wait()
				_ = hashPty.Close()

				// Save failed status with actual hash (if captured)
				if werr := saveIntegrityResult(imagePath, IntegrityEntry{ Type: "compressed", Method: "xz -tv", Status: "failed", CheckedAt: time.Now().Format(time.RFC3339), Actual: finalHash }); werr != nil {
					select { case progressChan <- ErrorMsg{Err: fmt.Errorf("failed to write integrity.yaml: %v", werr)}: default: }
				} else {
					select { case progressChan <- ProgressMsg(fmt.Sprintf("Saved integrity record to %s", filepath.Join(filepath.Dir(imagePath), "integrity.yaml"))): default: }
				}
				select { case progressChan <- CheckCompletedMsg{File: imagePath, Ok: false}: default: }
				return
			}

			// Raw image
			status := "computed"
			ok := false
			if haveExpected && finalHash != "" && strings.EqualFold(finalHash, expectedFromSidecar) && err == nil {
				status = "ok"
				ok = true
			} else if haveExpected {
				status = "failed"
			}
			if werr := saveIntegrityResult(imagePath, IntegrityEntry{ Type: "raw", Method: "sha256sum", Status: status, CheckedAt: time.Now().Format(time.RFC3339), Expected: expectedFromSidecar, Actual: finalHash }); werr != nil {
				select { case progressChan <- ErrorMsg{Err: fmt.Errorf("failed to write integrity.yaml: %v", werr)}: default: }
			} else {
				select { case progressChan <- ProgressMsg(fmt.Sprintf("Saved integrity record to %s", filepath.Join(filepath.Dir(imagePath), "integrity.yaml"))): default: }
			}
			select { case progressChan <- CheckCompletedMsg{File: imagePath, Ok: ok}: default: }
		}()

		return nil
	}
}

// --- integrity.yaml persistence ---

type IntegrityFile struct { Files map[string]IntegrityEntry `yaml:"files"` }

type IntegrityEntry struct {
	Type      string `yaml:"type"`
	Method    string `yaml:"method"`
	Status    string `yaml:"status"`
	CheckedAt string `yaml:"checked_at"`
	Expected  string `yaml:"expected,omitempty"`
	Actual    string `yaml:"actual,omitempty"`
}

func saveIntegrityResult(imagePath string, entry IntegrityEntry) error {
	dir := filepath.Dir(imagePath)
	yamlPath := filepath.Join(dir, "integrity.yaml")

	var doc IntegrityFile
	if b, err := os.ReadFile(yamlPath); err == nil {
		_ = yaml.Unmarshal(b, &doc)
	}
	if doc.Files == nil { doc.Files = make(map[string]IntegrityEntry) }
	doc.Files[filepath.Base(imagePath)] = entry

	out, err := yaml.Marshal(&doc)
	if err != nil { return err }
	tmp := yamlPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil { return err }
	return os.Rename(tmp, yamlPath)
}

func ternary[T any](cond bool, a, b T) T { if cond { return a }; return b }
