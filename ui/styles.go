package ui

import (
	"github.com/charmbracelet/lipgloss"
)

const (
	// Color constants
	ColorBackground = "#201F24" // Blackish
	ColorWhite      = "#FFFFFF"
	ColorPantone    = "#D0112B" // Pantone 186C
	ColorLilac      = "#718CFD"
	ColorAnthracite = "#2F303B"
	ColorLightRed   = "#ED3B42"
	ColorError      = "#FF3333" // Bright red for errors

	// Minimal width for each selection window.
	MinListWidth = 50
)

// Styles returns common styles used in the UI
func Styles() struct {
	Header      lipgloss.Style
	Container   lipgloss.Style
	Active      lipgloss.Style
	Inactive    lipgloss.Style
	Button      lipgloss.Style
	FlashButton lipgloss.Style
	AbortButton lipgloss.Style
	FooterStyle lipgloss.Style
	InfoPanel   lipgloss.Style
	ViewportProgress lipgloss.Style
	SelectedBadge lipgloss.Style
} {
	return struct {
		Header      lipgloss.Style
		Container   lipgloss.Style
		Active      lipgloss.Style
		Inactive    lipgloss.Style
		Button      lipgloss.Style
		FlashButton lipgloss.Style
		AbortButton lipgloss.Style
		FooterStyle lipgloss.Style
		InfoPanel   lipgloss.Style
		ViewportProgress lipgloss.Style
		SelectedBadge lipgloss.Style
	}{
		Header: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(ColorWhite)).
			Background(lipgloss.Color(ColorPantone)).
			Align(lipgloss.Center).
			Padding(0, 0),
		
		Container: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(ColorLilac)).
			Padding(0, 0),
		
		Active: lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color(ColorPantone)),
		
		Inactive: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(ColorAnthracite)),
		
		Button: lipgloss.NewStyle().
			Bold(true).
			Padding(1, 1).
			Margin(1, 1).
			Foreground(lipgloss.Color(ColorWhite)),
		
		FlashButton: lipgloss.NewStyle().
			Bold(true).
			Padding(1, 1).
			Margin(1, 1).
			Foreground(lipgloss.Color(ColorWhite)),
		
		AbortButton: lipgloss.NewStyle().
			Bold(true).
			Padding(1, 1).
			Margin(1, 1).
			Foreground(lipgloss.Color(ColorWhite)),
		
		FooterStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorWhite)).
			Align(lipgloss.Center).
			MarginTop(1),
		
		InfoPanel: lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorWhite)).
			Padding(0, 1),
			
		ViewportProgress: lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorWhite)).
			Padding(0, 1).
			MarginTop(0).
			Align(lipgloss.Right),

		SelectedBadge: lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorWhite)).
			Background(lipgloss.Color(ColorPantone)).
			Bold(true).
			Padding(0, 1),
	}
}
