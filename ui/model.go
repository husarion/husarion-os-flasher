package ui

import (
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
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
	Zones             *zone.Manager // Add zone manager to the model
	OsImgPath         string        // Store the image path for refreshes
	FlashStartTime    time.Time     // Track when flashing started
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
	isError := strings.HasPrefix(msg, "Error:") || strings.Contains(msg, "error")
	
	// Apply red styling to error messages
	if isError {
		// Style with red text
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

	// Update the viewport content with all logs
	m.Viewport.SetContent("Logs:\n" + strings.Join(m.Logs, "\n"))
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

	if msg.Button == tea.MouseButtonWheelUp {
		if m.ActiveList == 0 {
			keyMsg = tea.KeyMsg{Type: tea.KeyDown}
			m.DeviceList, _ = m.DeviceList.Update(keyMsg)
		} else if m.ActiveList == 1 {
			keyMsg = tea.KeyMsg{Type: tea.KeyUp}
			m.ImageList, _ = m.ImageList.Update(keyMsg)
		} else if m.ActiveList == 2 {
			keyMsg = tea.KeyMsg{Type: tea.KeyUp}
			m.Viewport.Update(keyMsg)
			return m, nil
		}
	} else if msg.Button == tea.MouseButtonWheelDown {
		if m.ActiveList == 0 {
			keyMsg = tea.KeyMsg{Type: tea.KeyUp}
			m.DeviceList, _ = m.DeviceList.Update(keyMsg)
		} else if m.ActiveList == 1 {
			keyMsg = tea.KeyMsg{Type: tea.KeyDown}
			m.ImageList, _ = m.ImageList.Update(keyMsg)
		} else if m.ActiveList == 2 {
			keyMsg = tea.KeyMsg{Type: tea.KeyDown}
			m.Viewport.Update(keyMsg)
			return m, nil
		}
	}

	return m, nil
}
