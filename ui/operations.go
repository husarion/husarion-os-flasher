package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/husarion/husarion-os-flasher/util"
)

// StartFlashing initiates the flashing process
func (m *Model) StartFlashing() (tea.Model, tea.Cmd) {
	if m.DeviceList.SelectedItem() == nil || m.ImageList.SelectedItem() == nil || m.Flashing {
		return m, nil
	}

	imagePath := m.ImageList.SelectedItem().(Item).value
	devicePath := m.DeviceList.SelectedItem().(Item).value

	// Create a new progress channel for this run
	m.ProgressChan = make(chan tea.Msg)
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
				err := m.ExtractCmd.Process.Kill()
				if err != nil {
					return ErrorMsg{Err: fmt.Errorf("error aborting extraction: %v", err)}
				}
				return AbortCompletedMsg{}
			}),
		)
	}
	
	m.AddLog("No operation to abort.")
	return m, nil
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

	// Set focus to the Abort button based on system type
	if util.IsRaspberryPi() {
		m.ActiveList = 6 // Abort button index on Pi
	} else {
		m.ActiveList = 5 // Abort button index on non-Pi
	}

	// Immediately return a dummy command to update the UI
	return m, tea.Sequence(
		func() tea.Msg {
			// This first message just ensures the UI updates right away
			return ProgressMsg("Starting extraction...")
		},
		func() tea.Msg {
			// Run the extraction in a goroutine
			go func() {
				// Create command without -k flag to force overwrite
				cmd := exec.Command("xz", "-d", "-f", compressedPath)
				
				// Store command reference for aborting
				m.ExtractCmd = cmd
				
				// Start the command and capture output
				output, err := cmd.CombinedOutput()
				
				if err != nil {
					// If there's an error, send it to the progress channel
					m.ProgressChan <- ErrorMsg{Err: fmt.Errorf("failed to uncompress: %v\n%s", err, output)}
				} else {
					// If successful, send completion message
					m.ProgressChan <- ExtractCompletedMsg{
						Src: compressedPath,
						Dst: outputPath,
					}
				}
			}()
			
			// Return nil to chain to the next command
			return nil
		},
		// Listen for progress messages
		ListenProgress(m.ProgressChan),
	)
}
