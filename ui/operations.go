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
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/creack/pty"
	"github.com/husarion/husarion-os-flasher/util"
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
			tea.Tick(10*time.Millisecond, func(time.Time) tea.Msg { 
				return nil 
			}),
			tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
				// Kill the process
				err := m.ExtractCmd.Process.Kill()
				if err != nil {
					return ErrorMsg{Err: fmt.Errorf("error aborting extraction: %v", err)}
				}
				// Close the pty to ensure proper cleanup
				if m.ExtractPty != nil {
					m.ExtractPty.Close()
				}
				// Don't close the progress channel here - let the goroutine handle it
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
		// Key fix: use dd to write the file so pv's stderr progress goes to pty, not lost to redirection
		var cmd *exec.Cmd
		if uncompressedSize > 0 {
			// Use pv with size parameter for accurate progress reporting (like flashing does)
			progressChan <- ProgressMsg(fmt.Sprintf("Extracting (size: %s)...", util.FormatBytes(uncompressedSize)))
			cmd = exec.Command("bash", "-c", fmt.Sprintf("set -o pipefail; xz -dc '%s' | pv -f -s %d | dd of='%s' bs=1k", 
				compressedPath, uncompressedSize, outputPath))
		} else {
			// Fallback if we couldn't determine the size
			progressChan <- ProgressMsg("Extracting (no size info)...")
			cmd = exec.Command("bash", "-c", fmt.Sprintf("set -o pipefail; xz -dc '%s' | pv -f | dd of='%s' bs=1k", 
				compressedPath, outputPath))
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
				// Safe send to progress channel
				select {
				case progressChan <- ErrorMsg{Err: fmt.Errorf("extraction failed: %v", err)}:
				default:
					// Channel might be closed, exit gracefully
					return
				}
			} else {
				// Get the actual final extracted file size
				if finalInfo, err := os.Stat(outputPath); err == nil {
					finalSize := finalInfo.Size()
					select {
					case progressChan <- ProgressMsg(fmt.Sprintf("Extraction complete. Final size: %s", util.FormatBytes(finalSize))):
					default:
						return
					}
				}
				
				select {
				case progressChan <- ExtractCompletedMsg{
					Src: compressedPath,
					Dst: outputPath,
				}:
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
