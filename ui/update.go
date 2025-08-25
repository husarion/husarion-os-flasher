package ui

import (
	"fmt"
	"io"
	"os/exec"
	"os/user"
	"path/filepath"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	
	"github.com/husarion/husarion-os-flasher/util"
)

// wrappingDelegate is a custom list delegate that intelligently truncates long text
type wrappingDelegate struct {
	list.DefaultDelegate
}

// newWrappingDelegate creates a new wrapping delegate
func newWrappingDelegate() wrappingDelegate {
	d := wrappingDelegate{
		DefaultDelegate: list.NewDefaultDelegate(),
	}
	return d
}

// smartTruncate intelligently truncates long filenames to show the most relevant parts
func smartTruncate(text string, maxWidth int) string {
	if len(text) <= maxWidth {
		return text
	}
	
	// For filenames, prioritize showing the beginning and end
	if maxWidth < 10 {
		return text[:maxWidth-3] + "..."
	}
	
	// Show first part + "..." + last part
	prefixLen := maxWidth/2 - 2
	suffixLen := maxWidth - prefixLen - 3
	
	if prefixLen > 0 && suffixLen > 0 {
		return text[:prefixLen] + "..." + text[len(text)-suffixLen:]
	}
	
	return text[:maxWidth-3] + "..."
}

// Render renders the list item with intelligent truncation for long titles
func (d wrappingDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	realItem := item.(Item)
	
	// Get the actual width from the list model
	listWidth := m.Width()
	
	// Calculate available width for the filename (subtract padding and decorations)
	availableWidth := listWidth - 15 // More padding for borders, selections, etc.
	if availableWidth < 15 {
		availableWidth = 15 // Minimum reasonable width
	}
	
	// Intelligently truncate the title if it's too long
	truncatedTitle := smartTruncate(realItem.title, availableWidth)
	
	// Create a new item with the truncated title
	truncatedItem := Item{
		title: truncatedTitle,
		value: realItem.value,
		desc:  realItem.desc,
	}
	
	// Use the default delegate to render with the truncated title
	d.DefaultDelegate.Render(w, m, index, truncatedItem)
}

// NewModel creates a new model for the application
func NewModel(osImgPath string, termWidth, termHeight int) Model {
	currentUser, _ := user.Current()
	if currentUser.Uid != "0" {
		return Model{Err: fmt.Errorf("this program must be run as root")}
	}

	// Fallback sizes to avoid zero-width/height screens (e.g., SSH PTY reports 0x0)
	if termWidth <= 0 {
		termWidth = MinListWidth
	}
	if termHeight <= 0 {
		termHeight = 20
	}

	// Get available devices and images
	devices, err := GetAvailableDevices()
	if err != nil {
		return Model{Err: err}
	}
	images, err := GetImageFiles(osImgPath)
	if err != nil {
		return Model{Err: err}
	}

	var deviceItems []list.Item
	for _, dev := range devices {
		deviceItems = append(deviceItems, Item{title: dev, value: dev, desc: "Storage Device"})
	}

	var imageItems []list.Item
	for _, img := range images {
		imageItems = append(imageItems, Item{title: filepath.Base(img), value: img, desc: "OS Image"})
	}

	// Use default delegate for devices, custom truncating delegate for images
	deviceDelegate := list.NewDefaultDelegate()
	imageDelegate := newWrappingDelegate() // Intelligent truncation

	// Calculate fixed widths for horizontal layout
	listWidth := termWidth / 2
	if listWidth < 30 {
		listWidth = 30 // Minimum width
	}

	deviceList := list.New(deviceItems, deviceDelegate, listWidth, 7)
	deviceList.Title = "  Select Target Device  "
	deviceList.SetShowTitle(true)
	deviceList.SetShowHelp(false)
	deviceList.SetFilteringEnabled(false)
	deviceList.Styles.Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(ColorWhite)).
		Background(lipgloss.Color(ColorPantone)).
		Padding(0, 1)

	imageList := list.New(imageItems, imageDelegate, listWidth, 7)
	imageList.Title = "    Select Image File   "
	imageList.SetShowTitle(true)
	imageList.SetShowHelp(false)
	imageList.SetFilteringEnabled(false)
	imageList.Styles.Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(ColorWhite)).
		Background(lipgloss.Color(ColorPantone)).
		Padding(0, 1)

	viewport := viewport.New(termWidth, 7)
	viewport.SetContent("Logs:\n")

	return Model{
		DeviceList:    deviceList,
		ImageList:     imageList,
		Logs:          make([]string, 0),
		Tick:          time.Now(),
		ActiveList:    0,  // Starting with device list selected
		ProgressChan:  make(chan tea.Msg),
		Width:         termWidth,
		Height:        termHeight,
		Zones:         zone.New(), // Initialize zone manager
		Viewport:      viewport,
		OsImgPath:     osImgPath,
		Extracting:    false,  // Initialize extraction state
	}
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

// Update updates the model based on messages
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	
	// Update ready state at the beginning of every update
	m.Ready = (m.DeviceList.SelectedItem() != nil && m.ImageList.SelectedItem() != nil)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Guard against zero values occasionally reported by some PTYs
		if msg.Width > 0 {
			m.Width = msg.Width
		} else if m.Width <= 0 {
			m.Width = MinListWidth
		}
		if msg.Height > 0 {
			m.Height = msg.Height
		} else if m.Height <= 0 {
			m.Height = 20
		}

		// Update viewport width
		vw := m.Width - 4
		if vw < 10 {
			vw = 10
		}
		m.Viewport.Width = vw
		
		// Update list widths to be fixed and equal
		listWidth := m.Width / 2
		if listWidth < 30 {
			listWidth = 30 // Minimum width
		}
		m.DeviceList.SetSize(listWidth, m.DeviceList.Height())
		m.ImageList.SetSize(listWidth, m.ImageList.Height())
		
		return m, nil

	case TickMsg:
		m.Refresh()
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg {
			return TickMsg(t)
		})

	case ProgressMsg:
		m.AddLog(string(msg))
		// Continue listening for progress messages during any long-running action
		if m.Flashing || m.Extracting || m.Checking {
			return m, ListenProgress(m.ProgressChan)
		}
		return m, nil

	case DoneMsg:
		m.Flashing = false
		m.Aborting = false  // Reset aborting state
		
		// Calculate flashing duration
		duration := time.Since(m.FlashStartTime)
		
		// Create a success message with image and device details
		var successMsg string
		if msg.Src != "" && msg.Dst != "" {
			// Format the success message with the source filename (not full path), destination, and duration
			srcName := filepath.Base(msg.Src)
			successMsg = fmt.Sprintf("%s flashed successfully to %s in %s", 
				srcName, 
				msg.Dst, 
				util.FormatDuration(duration))
		} else {
			// Fallback if source/destination info is missing
			successMsg = fmt.Sprintf("Flashing completed successfully in %s!", util.FormatDuration(duration))
		}
		
		// Apply green styling to the success message
		successMsg = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00FF00")).
			Bold(true).
			Render(successMsg)
		
		m.AddLog(successMsg)
		m.DdCmd = nil
		m.DdPty = nil  // Clear pty reference after completion
		return m, nil

	case ErrorMsg:
		m.Flashing = false
		m.Aborting = false
		m.ConfiguringEeprom = false
		m.Extracting = false
		m.Checking = false
		m.AddLog(fmt.Sprintf("Error: %v", msg.Err))
		m.DdCmd = nil
		m.ExtractCmd = nil
		m.CheckCmd = nil
		m.DdPty = nil
		m.ExtractPty = nil
		m.CheckPty = nil
		return m, nil

	case DDStartedMsg:
		m.DdCmd = msg.Cmd
		m.DdPty = msg.Pty
		// Continue listening for progress messages.
		return m, ListenProgress(m.ProgressChan)

	case ExtractStartedMsg:
		m.ExtractCmd = msg.Cmd
		m.ExtractPty = msg.Pty
		// Continue listening for progress messages and also send an immediate progress message
		m.AddLog("Extraction started - monitoring progress...")
		return m, tea.Batch(
			ListenProgress(m.ProgressChan),
			func() tea.Msg {
				return ProgressMsg("Initializing extraction...")
			},
		)

	case ExtractCompletedMsg:
		m.Extracting = false
		m.ExtractCmd = nil  // Clear command reference after completion
		m.ExtractPty = nil  // Clear pty reference after completion
		
		// Calculate extraction duration
		duration := time.Since(m.ExtractStartTime)
		
		// Create a success message with source, destination, and duration
		successMsg := fmt.Sprintf("%s successfully extracted to %s in %s", 
			filepath.Base(msg.Src), 
			filepath.Base(msg.Dst),
			util.FormatDuration(duration))
		successMsg = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00FF00")).
			Bold(true).
			Render(successMsg)
		
		m.AddLog(successMsg)
		
		// Refresh the image list
		return m, func() tea.Msg {
			return TickMsg(time.Now())
		}

	case CheckStartedMsg:
		m.CheckCmd = msg.Cmd
		m.CheckPty = msg.Pty
		m.AddLog("Integrity check started - monitoring progress...")
		return m, ListenProgress(m.ProgressChan)

	case CheckCompletedMsg:
		m.Checking = false
		m.CheckCmd = nil
		m.CheckPty = nil
		if msg.Ok {
			m.AddLog(lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00")).Bold(true).Render("Integrity OK"))
		} else {
			m.AddLog(lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")).Bold(true).Render("Integrity FAILED"))
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKeyMsg(msg)

	case tea.MouseMsg:
		return m.handleMouseMsg(msg)

	case EEPROMConfigMsg:
		for _, line := range msg.Output {
			if line != "" { // Skip empty lines
				m.AddLog(line)
			}
		}
		m.ConfiguringEeprom = false
		return m, nil
		
	case AbortCompletedMsg:
		m.Flashing = false
		m.Extracting = false
		m.Checking = false
		m.Aborting = false
		m.DdCmd = nil
		m.ExtractCmd = nil
		m.CheckCmd = nil
		m.DdPty = nil
		m.ExtractPty = nil
		m.CheckPty = nil
		m.AddLog(lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFCC00")).
			Bold(true).
			Render("Operation aborted by user"))
		return m, nil
	}

	return m, tea.Batch(cmds...)
}

// handleKeyMsg handles keyboard input
func (m Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc": // hit Esc → run 'shutdown -Ph now' (requires root)
		// fire-and-forget so UI can exit immediately
		go func() {
			cmd := exec.Command("shutdown", "-Ph", "now")
			// optional: surface any error; omit if you prefer silence
			if err := cmd.Run(); err != nil {
				m.AddLog(fmt.Sprintf("shutdown failed: %v", err))
			}
		}()

		return m, tea.Quit
		
	case "q":
		return m, tea.Quit
		
	case "tab":
		// Cycle through UI elements
		return m.handleTab()
		
	case "enter":
		return m.handleEnter()
	}
	
	// Forward other keys (e.g., arrows) to the focused view
	switch m.ActiveList {
	case 0: // Device list
		var cmd tea.Cmd
		m.DeviceList, cmd = m.DeviceList.Update(msg)
		return m, cmd
	case 1: // Image list
		var cmd tea.Cmd
		m.ImageList, cmd = m.ImageList.Update(msg)
		return m, cmd
	case 2: // Viewport
		var cmd tea.Cmd
		vp, cmd := m.Viewport.Update(msg)
		m.Viewport = vp
		return m, cmd
	}
	
	return m, nil
}

// handleTab handles tab key navigation between UI elements
func (m Model) handleTab() (tea.Model, tea.Cmd) {
	// Start with the current active element
	currentActive := m.ActiveList
	
	// Base focusable elements are the lists and viewport
	validElements := []int{0, 1, 2}
	
	inOperation := m.Flashing || m.Extracting || m.Checking
	hasCompressedImage := m.IsCompressedImageSelected()
	isPi := util.IsRaspberryPi()

	if inOperation {
		// While an operation is running, only allow Abort among the buttons
		abortIndex := -1
		if isPi {
			if hasCompressedImage || m.Extracting || m.Checking {
				abortIndex = 6
			} else {
				abortIndex = 5
			}
		} else {
			if hasCompressedImage || m.Extracting || m.Checking {
				abortIndex = 5
			} else {
				abortIndex = 4
			}
		}
		validElements = append(validElements, abortIndex)
	} else {
		// When idle, Flash is focusable
		validElements = append(validElements, 3)
		// EEPROM on Pi
		if isPi {
			validElements = append(validElements, 4)
		}
		// Extract button only when compressed image is selected and not in operation
		if hasCompressedImage {
			if isPi {
				validElements = append(validElements, 5)
			} else {
				validElements = append(validElements, 4)
			}
		}
		// Add a virtual index for Check button to be navigable
		validElements = append(validElements, 7)
	}
	
	// Find the next valid element greater than current
	foundNext := false
	for i := 0; i < len(validElements); i++ {
		if validElements[i] > currentActive {
			m.ActiveList = validElements[i]
			foundNext = true
			break
		}
	}
	// Wrap around if needed
	if !foundNext {
		m.ActiveList = validElements[0]
	}
	return m, nil
}

// handleEnter handles enter key press based on the active element
func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	// Handle enter key based on which element is selected
	if m.ActiveList == 3 {
		// Flash button - only allow if not already in an operation and ready
		if !m.Flashing && !m.Extracting && m.Ready {
			return m.StartFlashing()
		}
	} else if m.ActiveList == 4 {
		// This could be either EEPROM config or Abort button
		if m.Flashing || m.Extracting {
			// If we're in an operation, this is the Abort button
			return m.AbortOperation()
		} else if util.IsRaspberryPi() {
			// Otherwise on Pi, this is the EEPROM button - only allow if not in operation
			if !m.ConfiguringEeprom {
				return m.ConfigEEPROM()
			}
		} else if m.IsCompressedImageSelected() {
			// On non-Pi systems, this is the Extract Button - only allow if not in operation
			if !m.Flashing && !m.Extracting {
				return m.UncompressImage()
			}
		}
	} else if (util.IsRaspberryPi() && m.ActiveList == 5 && !m.Flashing && !m.Extracting && !m.Checking) {
		// Extract button on Pi (only when not in an operation)
		if m.IsCompressedImageSelected() {
			return m.UncompressImage()
		}
	} else if m.ActiveList == 7 && !m.Flashing && !m.Extracting && !m.Checking {
		// Check button (virtual index)
		return m.StartIntegrityCheck()
	} else if (util.IsRaspberryPi() && m.ActiveList == 6) || (!util.IsRaspberryPi() && m.ActiveList == 5) {
		// This is the dedicated Abort button position
		return m.AbortOperation()
	}
	return m, nil
}

// handleMouseMsg handles mouse input
func (m Model) handleMouseMsg(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Handle mouse wheel events
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		return m.HandleMouseWheel(msg)
	}

	// Only process left button clicks
	if msg.Action == tea.MouseActionRelease || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	// Handle abort button clicks - make this the first check to prioritize it
	if m.Zones.Get("abort-button").InBounds(msg) {
		// Ensure we call abortOperation even if clicking from another UI element
		return m.AbortOperation()
	}

	// Handle flash button clicks
	if m.Zones.Get("flash-button").InBounds(msg) {
		// First set the flash button as the active element
		m.ActiveList = 3
		
		// Only allow flashing if not already in an operation
		if !m.Flashing && !m.Extracting && m.Ready {
			return m.StartFlashing()
		}
		return m, nil // Return after handling the flash button
	}

	// Handle uncompress button clicks
	if m.IsCompressedImageSelected() && m.Zones.Get("uncompress-button").InBounds(msg) {
		// Set appropriate focus index based on system
		if util.IsRaspberryPi() {
			m.ActiveList = 5
		} else {
			m.ActiveList = 4
		}
		
		// Only allow extraction if not already in an operation
		if !m.Flashing && !m.Extracting {
			return m.UncompressImage()
		}
		return m, nil // Return after handling the uncompress button
	}

	// Check button clicks
	if m.Zones.Get("check-button").InBounds(msg) {
		// Mark selection for proper highlighting
		m.ActiveList = 7
		// Only allow when idle
		if !m.Flashing && !m.Extracting && !m.Checking {
			return m.StartIntegrityCheck()
		}
		return m, nil
	}

	// Handle other element clicks
	if m.Zones.Get("eeprom-button").InBounds(msg) {
		// Only allow EEPROM configuration if not already in an operation
		if !m.Flashing && !m.Extracting && !m.ConfiguringEeprom {
			return m.ConfigEEPROM()
		}
		return m, nil
	}
	
	// Handle list selection
	if m.Zones.Get("device-view").InBounds(msg) {
		m.ActiveList = 0
	} else if m.Zones.Get("image-view").InBounds(msg) {
		m.ActiveList = 1
	} else if m.Zones.Get("viewport-view").InBounds(msg) {
		m.ActiveList = 2
	}

	return m, nil
}
