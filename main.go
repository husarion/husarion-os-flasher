package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
)

const (
	colorBackground = "#201F24" // Blackish
	colorWhite      = "#FFFFFF"
	colorPantone    = "#D0112B" // Pantone 186C
	colorLilac      = "#718CFD"
	colorAnthracite = "#2F303B"
	colorLightRed   = "#ED3B42"

	// Minimal width for each selection window.
	minListWidth = 100
)

type item struct {
	title string // Display name (for images, just the base filename)
	value string // Actual value (full path)
	desc  string
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.desc }
func (i item) FilterValue() string { return i.title }

type model struct {
	deviceList   list.Model
	imageList    list.Model
	ready        bool
	flashing     bool
	logs         []string
	err          error
	tick         time.Time
	activeList   int
	width        int
	height       int
	progressChan chan tea.Msg // For streaming dd logs
	ddCmd        *exec.Cmd    // dd command pointer for aborting
}

type progressMsg string
type doneMsg struct{}
type errorMsg struct{ err error }
type tickMsg time.Time

// ddStartedMsg carries the dd command pointer for aborting.
type ddStartedMsg struct {
	cmd *exec.Cmd
}

func initialModel() model {
	currentUser, _ := user.Current()
	if currentUser.Uid != "0" {
		return model{err: fmt.Errorf("this program must be run as root")}
	}

	// Get available devices and images.
	devices, err := getAvailableDevices()
	if err != nil {
		return model{err: err}
	}
	images, err := getImageFiles()
	if err != nil {
		return model{err: err}
	}

	var deviceItems []list.Item
	for _, dev := range devices {
		deviceItems = append(deviceItems, item{title: dev, value: dev, desc: "Storage Device"})
	}

	var imageItems []list.Item
	for _, img := range images {
		imageItems = append(imageItems, item{title: filepath.Base(img), value: img, desc: "OS Image"})
	}

	// Initial list dimensions (will be overridden by window size messages).
	width := minListWidth
	height := 10

	deviceDelegate := list.NewDefaultDelegate()
	deviceDelegate.Styles.SelectedTitle = deviceDelegate.Styles.SelectedTitle.Foreground(lipgloss.Color(colorPantone))
	deviceDelegate.Styles.SelectedDesc = deviceDelegate.Styles.SelectedDesc.Foreground(lipgloss.Color(colorPantone))

	imageDelegate := list.NewDefaultDelegate()
	imageDelegate.Styles.SelectedTitle = imageDelegate.Styles.SelectedTitle.Foreground(lipgloss.Color(colorPantone))
	imageDelegate.Styles.SelectedDesc = imageDelegate.Styles.SelectedDesc.Foreground(lipgloss.Color(colorPantone))

	deviceList := list.New(deviceItems, deviceDelegate, width, height)
	deviceList.Title = "  Select Target Device  "
	deviceList.SetShowTitle(true)
	deviceList.SetShowHelp(false)
	deviceList.SetFilteringEnabled(false)
	deviceList.Styles.Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(colorWhite)).
		Background(lipgloss.Color(colorPantone)).
		Padding(0, 1)

	imageList := list.New(imageItems, imageDelegate, width, height)
	imageList.Title = "   Select Image File   "
	imageList.SetShowTitle(true)
	imageList.SetShowHelp(false)
	imageList.SetFilteringEnabled(false)
	imageList.Styles.Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(colorWhite)).
		Background(lipgloss.Color(colorPantone)).
		Padding(0, 1)

	return model{
		deviceList:   deviceList,
		imageList:    imageList,
		logs:         make([]string, 0),
		tick:         time.Now(),
		activeList:   0,
		progressChan: make(chan tea.Msg),
	}
}

func (m *model) refresh() {
	devices, err := getAvailableDevices()
	if err == nil {
		var deviceItems []list.Item
		for _, dev := range devices {
			deviceItems = append(deviceItems, item{title: dev, value: dev, desc: "Storage Device"})
		}
		m.deviceList.SetItems(deviceItems)
	}

	images, err := getImageFiles()
	if err == nil {
		var imageItems []list.Item
		for _, img := range images {
			imageItems = append(imageItems, item{title: filepath.Base(img), value: img, desc: "OS Image"})
		}
		m.imageList.SetItems(imageItems)
	}
}

func (m model) Init() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Update ready state at the beginning of every update
	m.ready = (m.deviceList.SelectedItem() != nil && m.imageList.SelectedItem() != nil)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// If the terminal is very narrow, use full width for each list.
		if m.width < (minListWidth*2 + 6) {
			m.deviceList.SetWidth(m.width - 4)
			m.imageList.SetWidth(m.width - 4)
		} else {
			listWidth := (m.width - 6) / 2
			if listWidth < minListWidth {
				listWidth = minListWidth
			}
			m.deviceList.SetWidth(listWidth)
			m.imageList.SetWidth(listWidth)
		}
		return m, nil

	case tickMsg:
		m.refresh()
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg {
			return tickMsg(t)
		})

	case progressMsg:
		m.addLog(string(msg))
		if m.flashing {
			return m, listenProgress(m.progressChan)
		}
		return m, nil

	case doneMsg:
		m.flashing = false
		m.addLog("Done!")
		m.ddCmd = nil
		return m, nil

	case errorMsg:
		m.flashing = false
		m.addLog(fmt.Sprintf("Error: %v", msg.err))
		m.ddCmd = nil
		return m, nil

	case ddStartedMsg:
		m.ddCmd = msg.cmd
		// Continue listening for progress messages.
		return m, listenProgress(m.progressChan)

	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "right", "left":
			m.activeList = (m.activeList + 1) % 2
			return m, nil
		case "enter":
			return m.startFlashing()
		case "a", "A":
			return m.abortFlashing()
		case "ctrl+c", "q", "Q":
			return m, tea.Quit
		case "up", "down":
			if m.activeList == 0 {
				m.deviceList, _ = m.deviceList.Update(msg)
			} else {
				m.imageList, _ = m.imageList.Update(msg)
			}
		}

	case tea.MouseMsg:
		// Handle mouse wheel events
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			return m.handleMouseWheel(msg)
		}

		// Only process left button clicks
		if msg.Action == tea.MouseActionRelease || msg.Button != tea.MouseButtonLeft {
			return m, nil
		}

		// Handle flash button clicks
		if zone.Get("flash-button").InBounds(msg) {
			if !m.flashing {
				return m.startFlashing()
			} else {
				return m.abortFlashing()
			}
		}

		// Handle list selection
		if zone.Get("device-view").InBounds(msg) {
			m.activeList = 0
		} else if zone.Get("image-view").InBounds(msg) {
			m.activeList = 1
		}

		return m, nil
	}

	return m, nil
}

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\nPress q to quit\n", m.err)
	}

	// Build extra info panel for disk and image sizes.
	var diskInfo, imageInfo string
	if m.deviceList.SelectedItem() != nil {
		disk := m.deviceList.SelectedItem().(item).value
		size, err := getDiskSize(disk)
		if err != nil {
			diskInfo = disk + " (size: unknown)"
		} else {
			diskInfo = disk + " (size: " + formatBytes(size) + ")"
		}
	} else {
		diskInfo = "No disk selected"
	}
	if m.imageList.SelectedItem() != nil {
		image := m.imageList.SelectedItem().(item).value
		stat, err := os.Stat(image)
		if err != nil {
			imageInfo = image + " (size: unknown)"
		} else {
			imageInfo = image + " (size: " + formatBytes(stat.Size()) + ")"
		}
	} else {
		imageInfo = "No image selected"
	}
	infoPanel := lipgloss.NewStyle().Foreground(lipgloss.Color(colorWhite)).Padding(0, 1).
		Render("Disk: " + diskInfo + "\nImage: " + imageInfo)

	// Header.
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(colorWhite)).
		Background(lipgloss.Color(colorPantone)).
		Align(lipgloss.Center).
		Padding(1, 0)
	header := headerStyle.Render(" Husarion OS Flasher ")

	// Container for lists.
	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(colorLilac)).
		Padding(1, 2)

	activeStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color(colorPantone))
	inactiveStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(colorAnthracite))

	deviceView := m.deviceList.View()
	imageView := m.imageList.View()
	if m.activeList == 0 {
		deviceView = zone.Mark("device-view", activeStyle.Render(deviceView))
		imageView = zone.Mark("image-view", inactiveStyle.Render(imageView))
	} else {
		deviceView = zone.Mark("device-view", inactiveStyle.Render(deviceView))
		imageView = zone.Mark("image-view", activeStyle.Render(imageView))
	}

	var listView string
	if m.width < 80 {
		listView = lipgloss.JoinVertical(lipgloss.Center, deviceView, imageView)
	} else {
		listView = lipgloss.JoinHorizontal(lipgloss.Center, deviceView, imageView)
	}
	listView = containerStyle.Render(listView)

	// Flash button.
	var buttonStyle lipgloss.Style
	var buttonText string

	if m.flashing {
		buttonStyle = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 2).
			Margin(1, 0).
			Foreground(lipgloss.Color(colorWhite)).
			Background(lipgloss.Color(colorAnthracite))
		buttonText = "Abort"
	} else {
		buttonStyle = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 2).
			Margin(1, 0).
			Foreground(lipgloss.Color(colorWhite)).
			Background(lipgloss.Color(colorPantone))
		buttonText = "Flash Image"

		if !m.ready {
			buttonStyle = buttonStyle.Background(lipgloss.Color(colorAnthracite))
		}
	}

	button := zone.Mark("flash-button", buttonStyle.Render(buttonText))

	// Logs panel.
	logStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colorLightRed)).
		Padding(0, 1).
		MarginTop(1).
		Width(m.width - 4)
	logView := "Logs:\n" + strings.Join(m.logs, "\n")
	logPanel := logStyle.Render(logView)

	// Footer.
	footerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(colorWhite)).
		Align(lipgloss.Center).
		MarginTop(1)
	footer := footerStyle.Render("Press TAB to switch, ↑↓ to select the device/image, ENTER to flash, A to abort, Q to quit.")

	ui := lipgloss.JoinVertical(lipgloss.Center,
		header,
		listView,
		infoPanel,
		button,
		logPanel,
		footer,
	)

	// final := lipgloss.Place(
	// 	m.width,
	// 	m.height,
	// 	lipgloss.Center,
	// 	lipgloss.Center,
	// 	ui,
	// 	lipgloss.WithWhitespaceChars(" "),
	// 	lipgloss.WithWhitespaceBackground(lipgloss.Color(colorBackground)),
	// )
	final := lipgloss.Place(
		m.width,
		m.height,
		lipgloss.Center,
		lipgloss.Center,
		ui,
	)

	bgStyle := lipgloss.NewStyle()

	// bgStyle := lipgloss.NewStyle().
	// 	Background(lipgloss.Color(colorBackground)).
	// 	Foreground(lipgloss.Color(colorWhite))
	return zone.Scan(bgStyle.Render(final))
}

// getDiskSize returns the size (in bytes) of a disk using "blockdev --getsize64".
func getDiskSize(device string) (int64, error) {
	out, err := exec.Command("blockdev", "--getsize64", device).Output()
	if err != nil {
		return 0, err
	}
	sizeStr := strings.TrimSpace(string(out))
	return strconv.ParseInt(sizeStr, 10, 64)
}

// formatBytes returns a human-friendly string for a byte count.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func listenProgress(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

// Helper method for adding log entries with overflow protection
func (m *model) addLog(msg string) {
	m.logs = append(m.logs, msg)
	if len(m.logs) > 10 {
		m.logs = m.logs[1:]
	}
}

// Helper method for starting the flashing process
func (m *model) startFlashing() (tea.Model, tea.Cmd) {
	if m.deviceList.SelectedItem() == nil || m.imageList.SelectedItem() == nil || m.flashing {
		return m, nil
	}

	imagePath := m.imageList.SelectedItem().(item).value
	devicePath := m.deviceList.SelectedItem().(item).value

	// Create a new progress channel for this run
	m.progressChan = make(chan tea.Msg)
	m.flashing = true
	m.addLog(fmt.Sprintf("> Starting to flash %s to %s...", imagePath, devicePath))

	return m, tea.Batch(
		writeImage(imagePath, devicePath, m.progressChan),
		listenProgress(m.progressChan),
	)
}

// Helper method for aborting the flashing process
func (m *model) abortFlashing() (tea.Model, tea.Cmd) {
	if !m.flashing || m.ddCmd == nil {
		return m, nil
	}

	err := m.ddCmd.Process.Kill()
	if err != nil {
		m.addLog(fmt.Sprintf("Error aborting: %v", err))
	} else {
		m.addLog("Flashing aborted.")
	}

	m.flashing = false
	m.ddCmd = nil
	return m, nil
}

// Helper method for handling mouse wheel events
func (m *model) handleMouseWheel(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	var keyMsg tea.KeyMsg

	if msg.Button == tea.MouseButtonWheelUp {
		if m.activeList == 0 {
			keyMsg = tea.KeyMsg{Type: tea.KeyUp}
			m.deviceList, _ = m.deviceList.Update(keyMsg)
		} else {
			keyMsg = tea.KeyMsg{Type: tea.KeyDown}
			m.imageList, _ = m.imageList.Update(keyMsg)
		}
	} else if msg.Button == tea.MouseButtonWheelDown {
		if m.activeList == 0 {
			keyMsg = tea.KeyMsg{Type: tea.KeyDown}
			m.deviceList, _ = m.deviceList.Update(keyMsg)
		} else {
			keyMsg = tea.KeyMsg{Type: tea.KeyUp}
			m.imageList, _ = m.imageList.Update(keyMsg)
		}
	}

	return m, nil
}

func main() {
	currentUser, err := user.Current()
	zone.NewGlobal()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error retrieving user info:", err)
		os.Exit(1)
	}
	if currentUser.Uid != "0" {
		fmt.Fprintln(os.Stderr, "This program must be run as root.")
		os.Exit(1)
	}

	p := tea.NewProgram(initialModel(), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
