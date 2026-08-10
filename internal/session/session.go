package session

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/ahokinson/bloom/internal/config"
)

// Available reports whether bloom can run tmux at all.
//
// Worth asking up front, because every check below goes through tmux's exit
// status: a missing binary looks exactly like "no such session" unless somebody
// asks the question directly.
func Available() error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("bloom requires tmux: %w", err)
	}
	return nil
}

// Session represents a tmux session bound to a project directory.
type Session struct {
	Name      string
	Directory string

	// socket routes every command at a private tmux server. Only the tests set
	// it, so they cannot see — or kill — the sessions you are working in.
	socket string
}

// NewSession returns a Session for the given name and working directory.
func NewSession(name, directory string) *Session {
	return &Session{Name: name, Directory: directory}
}

// tmux builds a tmux invocation, aimed at this session's server.
func (s *Session) tmux(args ...string) *exec.Cmd {
	if s.socket != "" {
		args = append([]string{"-S", s.socket}, args...)
	}
	return exec.Command("tmux", args...)
}

// target names this session and only this session.
//
// tmux resolves a target as an exact name, then as the *start* of a name, then
// as a glob pattern. Bare names therefore hit the wrong session as soon as one
// name is a prefix of another: with api_v2 running, `has-session -t api` says
// yes, and `kill-session -t api` takes api_v2 down with whatever was in it. The
// "=" prefix asks for an exact match and nothing else.
func (s *Session) target() string {
	return "=" + s.Name
}

// window names one window in this session exactly. Both halves need the prefix —
// the window half prefix-matches too, so a bare "web" finds "webpack".
func (s *Session) window(name string) string {
	return "=" + s.Name + ":=" + name
}

// nextWindow targets this session with an empty window name, which is how tmux
// is told to pick the next free index.
func (s *Session) nextWindow() string {
	return "=" + s.Name + ":"
}

// Exists reports whether a tmux session with this name is running.
//
// An exit status is tmux answering the question; anything else — no binary, an
// unreachable server — is no answer at all. Reporting that as "no session" is
// how --kill comes to claim success having done nothing.
func (s *Session) Exists() (bool, error) {
	err := s.tmux("has-session", "-t", s.target()).Run()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return false, nil
	}
	return false, fmt.Errorf("checking for session %s: %w", s.Name, err)
}

// Create starts a new tmux session with a window for each tool.
func (s *Session) Create(tools []config.Tool) error {
	if len(tools) == 0 {
		return errors.New("no tools configured")
	}

	// -s and -n take names rather than targets, so they get no "=" prefix: that
	// would create a session literally called "=whatever".
	first := tools[0]
	cmd := s.tmux("new-session", "-d", "-s", s.Name, "-n", first.Name, "-c", s.Directory, first.Command)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("creating session %s: %w", s.Name, err)
	}

	for _, tool := range tools[1:] {
		if err := s.newWindow(tool); err != nil {
			return err
		}
	}

	return nil
}

// newWindow opens a window for the tool at the next free index.
func (s *Session) newWindow(tool config.Tool) error {
	cmd := s.tmux("new-window", "-t", s.nextWindow(),
		"-n", tool.Name, "-c", s.Directory, tool.Command)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("creating window %s: %w", tool.Name, err)
	}
	return nil
}

// SelectWindow focuses the named window within the session.
func (s *Session) SelectWindow(window string) error {
	return s.tmux("select-window", "-t", s.window(window)).Run()
}

// Windows lists the window names in the session, in index order.
func (s *Session) Windows() ([]string, error) {
	out, err := s.tmux("list-windows", "-t", s.target(), "-F", "#{window_name}").Output()
	if err != nil {
		return nil, fmt.Errorf("listing windows for %s: %w", s.Name, err)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// windowSet is Windows as a lookup, fetched once so a refresh over six tools
// does not shell out to tmux seven times.
func (s *Session) windowSet() (map[string]bool, error) {
	names, err := s.Windows()
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set, nil
}

// respawn restarts the tool's command in its existing window. -k kills whatever
// is running there first: a long-lived process will not pick up a changed
// command on its own, and without -k tmux refuses to reuse a live window.
func (s *Session) respawn(tool config.Tool) error {
	cmd := s.tmux("respawn-window", "-k",
		"-c", s.Directory, "-t", s.window(tool.Name), "--", tool.Command)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("respawning window %s: %w", tool.Name, err)
	}
	return nil
}

// Refresh re-runs the named tool in an existing session, or every tool when
// only is empty. A tool whose window is missing is created rather than skipped.
func (s *Session) Refresh(tools []config.Tool, only string) error {
	live, err := s.windowSet()
	if err != nil {
		return err
	}

	matched := false
	for _, tool := range tools {
		if only != "" && tool.Name != only {
			continue
		}
		matched = true
		if live[tool.Name] {
			if err := s.respawn(tool); err != nil {
				return err
			}
			continue
		}
		if err := s.newWindow(tool); err != nil {
			return err
		}
	}
	if only != "" && !matched {
		return fmt.Errorf("no tool named %q to refresh", only)
	}
	return nil
}

// Sync brings a live session up to date with the config by creating the windows
// it is missing. Anything already there is left alone, running process and all.
//
// This is the counterpart to Refresh, not a variant of it. Adding a tool to the
// config is the common case, and Refresh's answer — kill every window and start
// over — costs you whatever those windows were doing. Sync returns the names it
// created so the caller can say what changed.
func (s *Session) Sync(tools []config.Tool) ([]string, error) {
	have, err := s.windowSet()
	if err != nil {
		return nil, err
	}

	var created []string
	for _, tool := range tools {
		if have[tool.Name] {
			continue
		}
		if err := s.newWindow(tool); err != nil {
			return created, err
		}
		created = append(created, tool.Name)
	}
	return created, nil
}

// Send delivers text to a window without disturbing what is running in it, then
// submits it with Enter. An empty window targets whichever one is active.
//
// This goes through a paste buffer rather than send-keys because the text is
// very often multi-line. send-keys would deliver each newline as its own Enter,
// so a paragraph arrives as a dozen separate submissions — the first line to a
// prompt and the rest to whatever it turned into. paste-buffer -p wraps the lot
// in bracketed-paste markers, which is how a terminal app is told "this is one
// block of pasted text, not typing".
//
// The -- before the text stops tmux reading a leading dash in it as a flag of
// its own, so a message that happens to start with "--force" stays a message.
func (s *Session) Send(window, text string) error {
	target := s.target()
	if window != "" {
		// Checked against the live window list rather than left to the target to
		// sort out. Text going to the wrong program is the worst thing that can
		// happen here — it is a prompt answered by something that never asked —
		// so this does not rely solely on tmux honouring the exact-match prefix.
		live, err := s.windowSet()
		if err != nil {
			return err
		}
		if !live[window] {
			return fmt.Errorf("session %s has no window named %q", s.Name, window)
		}
		target = s.window(window)
	}

	// A file read from stdin ends in a newline. Pasting that and then pressing
	// Enter submits twice: once for the text, once for the empty line it left
	// behind. Trailing newlines are never the point of the message.
	text = strings.TrimRight(text, "\r\n")

	// Nothing left to paste. `--send ""` is still a real instruction — submit
	// whatever the window is already holding — and it needs no buffer at all.
	if text != "" {
		// Named per session so two bloom runs cannot consume each other's buffer.
		buffer := "bloom-" + s.Name
		if err := s.tmux("set-buffer", "-b", buffer, "--", text).Run(); err != nil {
			return fmt.Errorf("staging text for %s: %w", target, err)
		}
		// -d drops the buffer once pasted, so nothing is left sitting in tmux's
		// paste stack for the next person who hits prefix-].
		if err := s.tmux("paste-buffer", "-b", buffer, "-p", "-d", "-t", target).Run(); err != nil {
			// The buffer outlives a failed paste; clear it rather than leak it.
			_ = s.tmux("delete-buffer", "-b", buffer).Run()
			return fmt.Errorf("sending to %s: %w", target, err)
		}
	}

	if err := s.tmux("send-keys", "-t", target, "Enter").Run(); err != nil {
		return fmt.Errorf("submitting to %s: %w", target, err)
	}
	return nil
}

// Kill tears the session down and reports whether one was there to tear down. A
// session that is not running is not an error: the caller asked for it gone and
// it is gone.
func (s *Session) Kill() (bool, error) {
	exists, err := s.Exists()
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	if err := s.tmux("kill-session", "-t", s.target()).Run(); err != nil {
		return false, fmt.Errorf("killing session %s: %w", s.Name, err)
	}
	return true, nil
}

// AttachOrSwitch attaches to the session, or switches the current client to it
// when already inside tmux.
//
// attach-session refuses to nest ("sessions should be nested with care") when
// $TMUX is set, so running bloom from a tmux window would otherwise just fail.
func (s *Session) AttachOrSwitch() error {
	if os.Getenv("TMUX") != "" {
		if err := s.tmux("switch-client", "-t", s.target()).Run(); err != nil {
			return fmt.Errorf("switching to session %s: %w", s.Name, err)
		}
		return nil
	}
	return s.attach()
}

// attach replaces the current process with tmux, attaching to the session.
func (s *Session) attach() error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	args := []string{"tmux"}
	if s.socket != "" {
		args = append(args, "-S", s.socket)
	}
	args = append(args, "attach-session", "-t", s.target())
	return syscall.Exec(tmuxPath, args, os.Environ())
}
