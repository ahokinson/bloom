package session

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/ahokinson/bloom/internal/config"
)

// Session represents a tmux session bound to a project directory.
type Session struct {
	Name      string
	Directory string
}

// NewSession returns a Session for the given name and working directory.
func NewSession(name, directory string) *Session {
	return &Session{Name: name, Directory: directory}
}

// Exists reports whether a tmux session with this name is already running.
func (s *Session) Exists() bool {
	return exec.Command("tmux", "has-session", "-t", s.Name).Run() == nil
}

// Create starts a new tmux session with a window for each tool.
func (s *Session) Create(tools []config.Tool) error {
	if len(tools) == 0 {
		return fmt.Errorf("no valid tools configured")
	}

	first := tools[0]
	cmd := exec.Command("tmux", "new-session", "-d", "-s", s.Name, "-n", first.Name, "-c", s.Directory, first.Command)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("creating session %s: %w", s.Name, err)
	}

	for _, tool := range tools[1:] {
		// trailing colon: tmux assigns next window index
		cmd = exec.Command("tmux", "new-window", "-t", s.Name+":", "-n", tool.Name, "-c", s.Directory, tool.Command)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("creating window %s: %w", tool.Name, err)
		}
	}

	return nil
}

// SelectWindow focuses the named window within the session.
func (s *Session) SelectWindow(window string) error {
	return exec.Command("tmux", "select-window", "-t", s.Name+":"+window).Run()
}

// Attach replaces the current process with tmux, attaching to the session.
func (s *Session) Attach() error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	args := []string{"tmux", "attach-session", "-t", s.Name}
	return syscall.Exec(tmuxPath, args, os.Environ())
}
