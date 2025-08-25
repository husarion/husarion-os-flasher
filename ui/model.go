package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	"github.com/husarion/husarion-os-flasher/util"
)

// Model represents the application state
type Model struct {
	DeviceList        list.Model
	ImageList         list.Model
	Viewport          viewport.Model
	Ready             bool
	Flashing          bool
	Aborting          bool     // Track aborting state
	ConfiguringEeprom bool
	Extracting        bool     // Track when image extraction is in progress
	Logs              []string
	Err               error
	Tick              time.Time
	ActiveList        int
	Width             int
	Height            int
	ProgressChan      chan tea.Msg  // For streaming dd logs
	DdCmd             *exec.Cmd     // dd command pointer for aborting
	ExtractCmd        *exec.Cmd     // extraction command pointer for aborting
	DdPty             *os.File      // pty for dd command (for proper cleanup)
	ExtractPty        *os.File      // pty for extraction command (for proper cleanup)
	Zones             *zone.Manager // Add zone manager to the model
	OsImgPath         string        // Store the image path for refreshes
	FlashStartTime    time.Time     // Track when flashing started
	ExtractStartTime  time.Time     // Track when extraction started

	// Track current extraction file paths
	ExtractOutputPath string // final .img path
	ExtractTempPath   string // temporary .part path

	// Integrity check state
	Checking  bool
	CheckCmd  *exec.Cmd
	CheckPty  *os.File
}

// Item represents an entry in a list (device or image)
type Item struct {
	title string // Display name (for images, just the base filename)
	value string // Actual value (full path)
	desc  string
}

// Title implements the list.Item interface
func (i Item) Title() string { return i.title }

// Description implements the list.Item interface
func (i Item) Description() string { return i.desc }

// FilterValue implements the list.Item interface
func (i Item) FilterValue() string { return i.title }

// IsCompressedImageSelected checks if the selected image is a .img.xz file
func (m Model) IsCompressedImageSelected() bool {
	if m.ImageList.SelectedItem() == nil {
		return false
	}
	imagePath := m.ImageList.SelectedItem().(Item).value
	return strings.HasSuffix(imagePath, ".img.xz")
}

// AddLog adds a log entry with overflow protection
func (m *Model) AddLog(msg string) {
	// Check if this is an error message (starts with "Error:")
	lowerMsg := strings.ToLower(msg)
	isError := strings.HasPrefix(lowerMsg, "error:") || strings.Contains(lowerMsg, "error")
	
	// Apply red styling to error messages
	if isError {
		msg = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorError)).Render(msg)
	}

	// Check if this is a progress message from pv
	if strings.Contains(msg, "%") && strings.Contains(msg, "B/s") {
		// If we already have logs and the last one was a progress message,
		// replace it instead of adding a new log entry
		if len(m.Logs) > 0 && strings.Contains(m.Logs[len(m.Logs)-1], "%") &&
			strings.Contains(m.Logs[len(m.Logs)-1], "B/s") {
			m.Logs[len(m.Logs)-1] = msg // Replace the last progress entry
		} else {
			// First progress message or previous entry was not a progress message
			m.Logs = append(m.Logs, msg)
		}
	} else {
		// Regular log message, just append
		m.Logs = append(m.Logs, msg)
	}

	// Update the viewport content with all logs, applying word wrapping
	var wrappedLogs []string
	// Get the viewport width, minus some padding for borders
	logWidth := m.Viewport.Width - 2
	if logWidth < 10 {
		logWidth = 50 // Fallback minimum width
	}
	
	for _, log := range m.Logs {
		// Check if this log has ANSI color codes (styled text)
		hasColor := strings.Contains(log, "\x1b[")
		
		if hasColor {
			// Extract the style information and plain text
			plainText := stripANSI(log)
			wrapped := util.WrapText(plainText, logWidth)
			
			// Detect the original color from the log message
			var originalColor string
			if strings.Contains(log, "38;2;0;255;0") || strings.Contains(log, "\x1b[32m") {
				originalColor = "#00FF00" // Green
			} else if strings.Contains(log, "38;2;255;204;0") || strings.Contains(log, "\x1b[33m") || strings.Contains(log, "38;2;255;255;0") {
				originalColor = "#FFCC00" // Yellow
			} else if strings.Contains(log, "38;2;255;0;0") || strings.Contains(log, "\x1b[31m") {
				originalColor = "#FF0000" // Red
			} else {
				// Case-insensitive keyword heuristics
				p := strings.ToLower(plainText)
				if strings.Contains(p, "operation aborted") || strings.Contains(p, "aborted") {
					originalColor = "#FFCC00" // Yellow
				} else if strings.Contains(p, "successfully") || strings.Contains(p, "completed") || strings.Contains(p, "ok") {
					originalColor = "#00FF00" // Green
				} else if strings.Contains(p, "error") || strings.Contains(p, "failed") || strings.Contains(p, "failure") {
					originalColor = "#FF0000" // Red
				} else {
					originalColor = "#00FF00" // Fallback to green
				}
			}
			
			// Apply the original styling to each wrapped line
			wrappedLines := strings.Split(wrapped, "\n")
			var styledLines []string
			for _, line := range wrappedLines {
				if strings.TrimSpace(line) != "" {
					styledLine := lipgloss.NewStyle().
						Foreground(lipgloss.Color(originalColor)).
						Bold(true).
						Render(line)
					styledLines = append(styledLines, styledLine)
				}
			}
			wrappedLogs = append(wrappedLogs, strings.Join(styledLines, "\n"))
		} else {
			// Regular text, just wrap normally
			wrapped := util.WrapText(log, logWidth)
			wrappedLogs = append(wrappedLogs, wrapped)
		}
	}
	
	m.Viewport.SetContent("Logs:\n" + strings.Join(wrappedLogs, "\n"))
	m.Viewport.GotoBottom()
}

// Refresh updates the device and image lists
func (m *Model) Refresh() {
	devices, err := GetAvailableDevices()
	if err == nil {
		var deviceItems []list.Item
		for _, dev := range devices {
			deviceItems = append(deviceItems, Item{title: dev, value: dev, desc: "Storage Device"})
		}
		m.DeviceList.SetItems(deviceItems)
	}

	images, err := GetImageFiles(m.OsImgPath)
	if err == nil {
		var imageItems []list.Item
		for _, img := range images {
			imageItems = append(imageItems, Item{title: filepath.Base(img), value: img, desc: "OS Image"})
		}
		m.ImageList.SetItems(imageItems)
	}
}

// HandleMouseWheel handles mouse wheel events based on the active element
func (m *Model) HandleMouseWheel(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	var keyMsg tea.KeyMsg

	// Check if mouse is over the viewport area first - allow scrolling regardless of focus
	if m.Zones.Get("viewport-view").InBounds(msg) {
		// Try passing the mouse message directly to the viewport first
		var cmd tea.Cmd
		m.Viewport, cmd = m.Viewport.Update(msg)
		if cmd != nil {
			return m, cmd
		}
		
		// Fallback to keyboard events if mouse message doesn't work
		if msg.Button == tea.MouseButtonWheelUp {
			keyMsg = tea.KeyMsg{Type: tea.KeyUp}
			m.Viewport, _ = m.Viewport.Update(keyMsg)
			return m, nil
		} else if msg.Button == tea.MouseButtonWheelDown {
			keyMsg = tea.KeyMsg{Type: tea.KeyDown}
			m.Viewport, _ = m.Viewport.Update(keyMsg)
			return m, nil
		}
	}

	// Check if mouse is over device list area
	if m.Zones.Get("device-view").InBounds(msg) {
		if msg.Button == tea.MouseButtonWheelUp {
			keyMsg = tea.KeyMsg{Type: tea.KeyUp}
			m.DeviceList, _ = m.DeviceList.Update(keyMsg)
			return m, nil
		} else if msg.Button == tea.MouseButtonWheelDown {
			keyMsg = tea.KeyMsg{Type: tea.KeyDown}
			m.DeviceList, _ = m.DeviceList.Update(keyMsg)
			return m, nil
		}
	}

	// Check if mouse is over image list area
	if m.Zones.Get("image-view").InBounds(msg) {
		if msg.Button == tea.MouseButtonWheelUp {
			keyMsg = tea.KeyMsg{Type: tea.KeyUp}
			m.ImageList, _ = m.ImageList.Update(keyMsg)
			return m, nil
		} else if msg.Button == tea.MouseButtonWheelDown {
			keyMsg = tea.KeyMsg{Type: tea.KeyDown}
			m.ImageList, _ = m.ImageList.Update(keyMsg)
			return m, nil
		}
	}

	return m, nil
}

// stripANSI removes ANSI escape sequences from a string
func stripANSI(s string) string {
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return ansiRegex.ReplaceAllString(s, "")
}
