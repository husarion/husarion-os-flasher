package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/creack/pty"

	tea "github.com/charmbracelet/bubbletea"
)

func getImageFiles() ([]string, error) {
	var images []string

	entries, err := os.ReadDir("/os-images")
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".img" {
			images = append(images, filepath.Join("/os-images", entry.Name()))
		}
	}

	return images, nil
}

func writeImage(src, dst string, progressChan chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		// Start dd inside a pseudo-terminal so it flushes progress output in real time.
		cmd := exec.Command("dd", "if="+src, "of="+dst, "bs=1k", "status=progress")
		ptmx, err := pty.Start(cmd)
		if err != nil {
			progressChan <- errorMsg{err: fmt.Errorf("failed to start dd command: %v", err)}
			return nil
		}

		// Send ddStartedMsg so the model stores cmd for aborting.
		progressChan <- ddStartedMsg{cmd: cmd}

		go func() {
			scanner := bufio.NewScanner(ptmx)
			// Custom split function: split on carriage return OR newline.
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
					progressChan <- progressMsg(trimmed)
				}
			}

			if err := cmd.Wait(); err != nil {
				progressChan <- errorMsg{err: fmt.Errorf("dd command failed: %v", err)}
			} else {
				progressChan <- doneMsg{}
			}
		}()

		return nil
	}
}
