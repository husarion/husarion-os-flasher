package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/activeterm"
	"github.com/charmbracelet/wish/bubbletea"
	"github.com/charmbracelet/wish/logging"
	
	"github.com/husarion/husarion-os-flasher/ui"
)

const (
	// Minimal width for each selection window.
	minListWidth = 50
)

func main() {
	currentUser, err := user.Current()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error retrieving user info:", err)
		os.Exit(1)
	}
	if currentUser.Uid != "0" {
		fmt.Fprintln(os.Stderr, "This program must be run as root.")
		os.Exit(1)
	}

	// Define and parse command-line flags
	sshPort := flag.Int("port", 2222, "Port number for SSH server (1-65535)")
	osImgPath := flag.String("os-img-path", ".", "Path to OS image files directory")

	// Validate port number
	if *sshPort < 1 || *sshPort > 65535 {
		fmt.Fprintf(os.Stderr, "Invalid port number: %d. Must be between 1-65535\n", *sshPort)
		os.Exit(1)
	}

	enableSsh := flag.Bool("enable-ssh", false, "Run in SSH server mode")
	flag.Parse()

	if !*enableSsh {
		// Regular mode - start the application directly
		// Provide non-zero fallback sizes to avoid blank screen on some terminals
		w, h := minListWidth, 20
		p := tea.NewProgram(ui.NewModel(*osImgPath, w, h), tea.WithAltScreen(), tea.WithMouseCellMotion())
		if _, err := p.Run(); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		// SSH server configuration
		sshServer, err := wish.NewServer(
			wish.WithAddress(fmt.Sprintf(":%d", *sshPort)), // SSH port
			wish.WithHostKeyPath(".ssh/id_ed25519"),
			wish.WithMiddleware(
				bubbletea.Middleware(func(s ssh.Session) (tea.Model, []tea.ProgramOption) {
					pty, _, _ := s.Pty() // Get terminal dimensions
					return ui.NewModel(*osImgPath, pty.Window.Width, pty.Window.Height), []tea.ProgramOption{
						tea.WithAltScreen(),       // Keep your existing options
						tea.WithMouseCellMotion(), // Keep mouse support
					}
				}),
				activeterm.Middleware(), // Bubble Tea apps usually require a PTY.
				logging.Middleware(),
			),
		)

		if err != nil {
			fmt.Println("Error creating server:", err)
			os.Exit(1)
		}

		done := make(chan os.Signal, 1)
		signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
		log.Info("Starting SSH server")

		// Start SSH server
		fmt.Println("Starting SSH server on port", *sshPort, "...")
		go func() {
			if err = sshServer.ListenAndServe(); err != nil {
				fmt.Println("Error starting server:", err)
				done <- nil
			}
		}()

		<-done

		log.Info("Stopping SSH server")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer func() { cancel() }()
		if err := sshServer.Shutdown(ctx); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
			log.Error("Could not stop server", "error", err)
		}
	}
}
