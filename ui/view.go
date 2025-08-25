package ui

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
	"github.com/charmbracelet/lipgloss"
	"github.com/husarion/husarion-os-flasher/util"
)

// View renders the UI
func (m Model) View() string {
	if m.Err != nil {
		return fmt.Sprintf("Error: %v\nPress q to quit\n", m.Err)
	}

	styles := Styles()

	// Clamp sizes to avoid blank content when width/height are zero
	if m.Width <= 0 {
		m.Width = MinListWidth
	}
	if m.Height <= 0 {
		m.Height = 20
	}

	// Build extra info panel for disk and image sizes.
	var diskInfo, imageInfo string
	if m.DeviceList.SelectedItem() != nil {
		disk := m.DeviceList.SelectedItem().(Item).value
		size, err := util.GetDiskSize(disk)
		if err != nil {
			diskInfo = disk + " (size: unknown)"
		} else {
			diskInfo = disk + " (size: " + util.FormatBytes(size) + ")"
		}
	} else {
		diskInfo = "No disk selected"
	}

	integrityStatus := "unknown"
	integrityActual := ""
	if m.ImageList.SelectedItem() != nil {
		image := m.ImageList.SelectedItem().(Item).value
		stat, err := os.Stat(image)
		if err != nil {
			imageInfo = image + " (size: unknown)"
		} else {
			imageInfo = image + " (size: " + util.FormatBytes(stat.Size()) + ")"
		}
		// Load integrity.yaml from the image's directory and look up status
		yamlPath := filepath.Join(filepath.Dir(image), "integrity.yaml")
		if b, err := os.ReadFile(yamlPath); err == nil {
			var doc IntegrityFile
			if yaml.Unmarshal(b, &doc) == nil && doc.Files != nil {
				if entry, ok := doc.Files[filepath.Base(image)]; ok {
					if entry.Status != "" {
						integrityStatus = entry.Status
					}
					if entry.Actual != "" {
						integrityActual = entry.Actual
					}
				}
			}
		}
	} else {
		imageInfo = "No image selected"
	}

	integrityLine := "Integrity check: " + integrityStatus
	if integrityActual != "" {
		integrityLine += ", actual: " + integrityActual
	}
	infoPanel := styles.InfoPanel.Render("Disk: " + diskInfo + "\nImage: " + imageInfo + "\n" + integrityLine)

	// Header
	header := styles.Header.Render(" Husarion OS Flasher ")

	// Mark active and inactive elements
	deviceView := m.DeviceList.View()
	imageView := m.ImageList.View()
	viewportView := m.Viewport.View()

	// Add explicit selection indicators (index/total)
	if m.DeviceList.FilterState() == 0 {
		// Only when not filtering
		if total := len(m.DeviceList.Items()); total > 0 && m.DeviceList.Index() >= 0 {
			deviceView = deviceView + "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color(ColorLilac)).Render(
				fmt.Sprintf("Selected device: %d/%d", m.DeviceList.Index()+1, total),
			)
		}
	}
	if m.ImageList.FilterState() == 0 {
		if total := len(m.ImageList.Items()); total > 0 && m.ImageList.Index() >= 0 {
			imageView = imageView + "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color(ColorLilac)).Render(
				fmt.Sprintf("Selected image: %d/%d", m.ImageList.Index()+1, total),
			)
		}
	}

	// Calculate scroll percentage
	scrollPercent := int(m.Viewport.ScrollPercent() * 100)
	viewportProgressView := styles.ViewportProgress.
		Width(m.Viewport.Width).
		Render(fmt.Sprintf("%d%%", scrollPercent))

	// Apply active/inactive styling based on ActiveList
	if m.ActiveList == 0 {
		deviceView = m.Zones.Mark("device-view", styles.Active.Render(deviceView))
		imageView = m.Zones.Mark("image-view", styles.Inactive.Render(imageView))
		viewportView = m.Zones.Mark("viewport-view", styles.Inactive.Render(viewportView))
	} else if m.ActiveList == 1 {
		deviceView = m.Zones.Mark("device-view", styles.Inactive.Render(deviceView))
		imageView = m.Zones.Mark("image-view", styles.Active.Render(imageView))
		viewportView = m.Zones.Mark("viewport-view", styles.Inactive.Render(viewportView))
	} else if m.ActiveList == 2 {
		deviceView = m.Zones.Mark("device-view", styles.Inactive.Render(deviceView))
		imageView = m.Zones.Mark("image-view", styles.Inactive.Render(imageView))
		viewportView = m.Zones.Mark("viewport-view", styles.Active.Render(viewportView))
	} else {
		deviceView = m.Zones.Mark("device-view", styles.Inactive.Render(deviceView))
		imageView = m.Zones.Mark("image-view", styles.Inactive.Render(imageView))
		viewportView = m.Zones.Mark("viewport-view", styles.Inactive.Render(viewportView))
	}

	// Combine lists based on window width
	var listView string
	if m.Width < 80 {
		listView = lipgloss.JoinVertical(lipgloss.Center, deviceView, imageView)
	} else {
		listView = lipgloss.JoinHorizontal(lipgloss.Center, deviceView, imageView)
	}
	listView = styles.Container.Render(listView)

	// Create buttons
	buttonView := m.renderButtons(styles)

	// Footer
	footer := styles.FooterStyle.Render("TAB to switch • ↑↓ to navigate • ENTER to select • ESC to power-off • Q to quit.")

	// Combine all elements
	ui := lipgloss.JoinVertical(lipgloss.Center,
		header,
		listView,
		infoPanel,
		buttonView,
		viewportView,
		viewportProgressView,
		footer,
	)

	// Place in the window
	final := lipgloss.Place(
		m.Width,
		m.Height,
		lipgloss.Center,
		lipgloss.Center,
		ui,
	)

	// Apply background style and zone scanning
	bgStyle := lipgloss.NewStyle()
	return m.Zones.Scan(bgStyle.Render(final))
}

// renderButtons creates and styles all the UI buttons based on current state
func (m Model) renderButtons(styles struct {
	Header           lipgloss.Style
	Container        lipgloss.Style
	Active           lipgloss.Style
	Inactive         lipgloss.Style
	Button           lipgloss.Style
	FlashButton      lipgloss.Style
	AbortButton      lipgloss.Style
	FooterStyle      lipgloss.Style
	InfoPanel        lipgloss.Style
	ViewportProgress lipgloss.Style
	SelectedBadge    lipgloss.Style
}) string {
	// Flash button styling
	var buttonStyle lipgloss.Style
	var buttonText string

	// Determine button text based on state
	if m.Flashing {
		buttonText = "Flashing..."
	} else {
		buttonText = "Flash"
	}
	
	// Base styles
	buttonStyle = styles.Button
	
	// Apply background color based on state and selection
	if m.Flashing || m.Extracting || m.Checking {
		buttonStyle = buttonStyle.Background(lipgloss.Color(ColorDisabled))
	} else if m.ActiveList == 3 {
		buttonStyle = buttonStyle.Background(lipgloss.Color(ColorPantone))
	} else {
		buttonStyle = buttonStyle.Background(lipgloss.Color(ColorAnthracite))
		if !m.Ready {
			buttonStyle = buttonStyle.Background(lipgloss.Color(ColorAnthracite))
		} else {
			buttonStyle = buttonStyle.Background(lipgloss.Color("#505050"))
		}
	}

	flashButton := m.Zones.Mark("flash-button", buttonStyle.Render(buttonText))

	// Initialize buttonView variable
	var buttonView string
	
	// Create abort button that appears during any operation
	var abortButton string
	if m.Flashing || m.Extracting || m.Checking {
		abortStyle := styles.AbortButton
		// Determine expected abort index based on layout
		abortIndex := -1
		if util.IsRaspberryPi() {
			if m.IsCompressedImageSelected() || m.Extracting || m.Checking {
				abortIndex = 6
			} else {
				abortIndex = 5
			}
		} else {
			if m.IsCompressedImageSelected() || m.Extracting || m.Checking {
				abortIndex = 5
			} else {
				abortIndex = 4
			}
		}

		var abortText string
		if m.Aborting {
			abortText = "Aborting..."
			abortStyle = abortStyle.Background(lipgloss.Color(ColorDisabled))
		} else {
			abortText = "   Abort   "
			if m.ActiveList == abortIndex {
				abortStyle = abortStyle.Background(lipgloss.Color(ColorLightRed))
			} else {
				abortStyle = abortStyle.Background(lipgloss.Color(ColorAnthracite))
			}
		}
		abortButton = m.Zones.Mark("abort-button", abortStyle.Render(abortText))
	}

	// Add uncompress button only when a compressed image is selected OR currently extracting
	var checkButton string
	if m.IsCompressedImageSelected() || m.Extracting {
		uncompressStyle := styles.Button
		var uncompressText string
		if m.Extracting {
			uncompressText = "Extracting..."
			uncompressStyle = uncompressStyle.Background(lipgloss.Color(ColorDisabled))
		} else {
			uncompressText = "Extract"
			if (util.IsRaspberryPi() && m.ActiveList == 5 && !m.Flashing && !m.Checking) || (!util.IsRaspberryPi() && m.ActiveList == 4 && !m.Flashing && !m.Checking) {
				uncompressStyle = uncompressStyle.Background(lipgloss.Color(ColorLilac))
			} else if m.Flashing || m.Checking {
				uncompressStyle = uncompressStyle.Background(lipgloss.Color(ColorDisabled))
			} else {
				uncompressStyle = uncompressStyle.Background(lipgloss.Color(ColorAnthracite))
			}
		}
		buttonUncompress := m.Zones.Mark("uncompress-button", uncompressStyle.Render(uncompressText))

		// Integrity Check button
		checkStyle := styles.Button
		var checkText string
		if m.Checking {
			checkText = "Checking..."
			checkStyle = checkStyle.Background(lipgloss.Color(ColorDisabled))
		} else {
			checkText = " Check "
			if m.ActiveList == 7 && !m.Flashing && !m.Extracting {
				checkStyle = checkStyle.Background(lipgloss.Color(ColorLilac))
			} else if m.Flashing || m.Extracting {
				checkStyle = checkStyle.Background(lipgloss.Color(ColorDisabled))
			} else {
				checkStyle = checkStyle.Background(lipgloss.Color(ColorAnthracite))
			}
		}
		checkButton = m.Zones.Mark("check-button", checkStyle.Render(checkText))

		if util.IsRaspberryPi() {
			eepromStyle := styles.Button
			var eepromText string
			if m.ConfiguringEeprom {
				eepromText = "Configuring..."
				eepromStyle = eepromStyle.Background(lipgloss.Color(ColorDisabled))
			} else {
				eepromText = "Config EEPROM"
				if m.ActiveList == 4 && !m.Flashing && !m.Extracting && !m.Checking {
					eepromStyle = eepromStyle.Background(lipgloss.Color(ColorLilac))
				} else if m.Flashing || m.Extracting || m.Checking {
					eepromStyle = eepromStyle.Background(lipgloss.Color(ColorDisabled))
				} else {
					eepromStyle = eepromStyle.Background(lipgloss.Color(ColorAnthracite))
				}
			}
			buttonEeprom := m.Zones.Mark("eeprom-button", eepromStyle.Render(eepromText))
			if m.Flashing || m.Extracting || m.Checking {
				buttonView = lipgloss.JoinHorizontal(lipgloss.Center, flashButton, buttonEeprom, buttonUncompress, checkButton, abortButton)
			} else {
				buttonView = lipgloss.JoinHorizontal(lipgloss.Center, flashButton, buttonEeprom, buttonUncompress, checkButton)
			}
		} else {
			if m.Flashing || m.Extracting || m.Checking {
				buttonView = lipgloss.JoinHorizontal(lipgloss.Center, flashButton, buttonUncompress, checkButton, abortButton)
			} else {
				buttonView = lipgloss.JoinHorizontal(lipgloss.Center, flashButton, buttonUncompress, checkButton)
			}
		}
	} else {
		// Raw .img branch (no Extract button)
		checkStyle := styles.Button
		var checkText string
		if m.Checking {
			checkText = "Checking..."
			checkStyle = checkStyle.Background(lipgloss.Color(ColorDisabled))
		} else {
			checkText = " Check "
			if m.ActiveList == 7 && !m.Flashing && !m.Extracting {
				checkStyle = checkStyle.Background(lipgloss.Color(ColorLilac))
			} else {
				checkStyle = checkStyle.Background(lipgloss.Color(ColorAnthracite))
			}
		}
		checkButton = m.Zones.Mark("check-button", checkStyle.Render(checkText))

		if util.IsRaspberryPi() {
			eepromStyle := styles.Button
			var eepromText string
			if m.ConfiguringEeprom {
				eepromText = "Configuring..."
				eepromStyle = eepromStyle.Background(lipgloss.Color(ColorDisabled))
			} else {
				eepromText = "Config EEPROM"
				if m.ActiveList == 4 && !m.Flashing && !m.Extracting && !m.Checking {
					eepromStyle = eepromStyle.Background(lipgloss.Color(ColorLilac))
				} else if m.Flashing || m.Extracting || m.Checking {
					eepromStyle = eepromStyle.Background(lipgloss.Color(ColorDisabled))
				} else {
					eepromStyle = eepromStyle.Background(lipgloss.Color(ColorAnthracite))
				}
			}
			buttonEeprom := m.Zones.Mark("eeprom-button", eepromStyle.Render(eepromText))
			if m.Flashing || m.Extracting || m.Checking {
				buttonView = lipgloss.JoinHorizontal(lipgloss.Center, flashButton, buttonEeprom, checkButton, abortButton)
			} else {
				buttonView = lipgloss.JoinHorizontal(lipgloss.Center, flashButton, buttonEeprom, checkButton)
			}
		} else {
			if m.Flashing || m.Extracting || m.Checking {
				buttonView = lipgloss.JoinHorizontal(lipgloss.Center, flashButton, checkButton, abortButton)
			} else {
				buttonView = lipgloss.JoinHorizontal(lipgloss.Center, flashButton, checkButton)
			}
		}
	}

	return buttonView
}
