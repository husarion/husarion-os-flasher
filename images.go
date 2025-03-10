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

func getImageFiles(osImgPath string) ([]string, error) {
	// Use osImgPath instead of hardcoded "/os-images"
	entries, err := os.ReadDir(osImgPath)
	if err != nil {
		return nil, err
	}

	var images []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".img" {
			images = append(images, filepath.Join(osImgPath, entry.Name()))
		}
	}

	return images, nil
}

func writeImage(src, dst string, progressChan chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		// Unmount all partitions under the selected device (e.g. /dev/sda -> /dev/sda1, /dev/sda2, etc.)
		progressChan <- progressMsg("Unmounting all partitions under " + dst + " if mounted...")

		// Check if the device is mounted before attempting to unmount
		checkCmd := exec.Command("sh", "-c", "mount | grep "+dst)
		if err := checkCmd.Run(); err == nil {
			// Device is mounted, proceed to unmount
			if err := exec.Command("sh", "-c", "umount "+dst+"*").Run(); err != nil {
				progressChan <- progressMsg("Unmount error (ignored): " + err.Error())
			}
		} else {
			progressChan <- progressMsg("No partitions to unmount under " + dst)
		}

		// Start dd inside a pseudo-terminal so it flushes progress output in real time.
		cmd := exec.Command("sh", "-c", fmt.Sprintf("pv %s | dd of=%s bs=1k", src, dst))
		ptmx, err := pty.Start(cmd)
		if err != nil {
			progressChan <- errorMsg{err: fmt.Errorf("failed to start dd command: %v", err)}
			return nil
		}

		// Send ddStartedMsg so the model stores the dd command pointer for aborting.
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
				progressChan <- progressMsg("Syncing...")
				if err := exec.Command("sync").Run(); err != nil {
					progressChan <- errorMsg{err: fmt.Errorf("sync failed: %v", err)}
				} else {
					progressChan <- progressMsg("Sync completed successfully.")
					progressChan <- doneMsg{}
				}
			}
		}()

		return nil
	}
}
