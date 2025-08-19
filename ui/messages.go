package ui

import (
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Message types for the UI
type (
	// ProgressMsg is sent with progress updates during flashing or extraction
	ProgressMsg string
	
	// DoneMsg is sent when flashing is complete
	DoneMsg struct {
		Src string
		Dst string
	}
	
	// ErrorMsg is sent when an error occurs
	ErrorMsg struct{ Err error }
	
	// TickMsg is sent periodically to update UI
	TickMsg time.Time
	
	// DDStartedMsg carries the dd command pointer for aborting
	DDStartedMsg struct {
		Cmd *exec.Cmd
	}
	
	// EEPROMConfigMsg is sent with EEPROM configuration results
	EEPROMConfigMsg struct {
		Output []string
	}
	
	// AbortCompletedMsg is sent when an abort action is complete
	AbortCompletedMsg struct{}
	
	// ExtractCompletedMsg is sent when extraction is complete
	ExtractCompletedMsg struct {
		Src string
		Dst string
	}
	
	// ExtractStartedMsg is sent when extraction starts
	ExtractStartedMsg struct {
		Cmd *exec.Cmd
	}
)

// ListenProgress returns a command that listens for messages on a channel
func ListenProgress(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}
