package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ahokinson/bloom/internal/config"
)

// These tests drive a real tmux. Mocking it would only prove that bloom builds
// the arguments bloom thinks are right, and every bug this file guards against
// was a wrong assumption about what tmux does with them.
//
// Everything runs on a private socket, so a test run cannot see — let alone kill
// — the sessions you are working in.
type server struct {
	t      *testing.T
	socket string
}

func newServer(t *testing.T) *server {
	t.Helper()

	if testing.Short() {
		t.Skip("tmux integration test skipped by -short")
	}
	if err := Available(); err != nil {
		t.Skipf("tmux integration test skipped: %v", err)
	}

	// A unix socket path is capped near 104 bytes on macOS, and t.TempDir()
	// bakes the test name into the path — long enough to blow that budget on its
	// own. MkdirTemp keeps it short.
	dir, err := os.MkdirTemp("", "bloomtest")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "s")

	srv := &server{t: t, socket: socket}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", socket, "kill-server").Run()
		_ = os.RemoveAll(dir)
	})

	// Prove the server is actually usable before any test asserts on tmux's
	// behaviour, or an environment that forbids binding a unix socket produces a
	// dozen failures that all read like bugs in bloom.
	//
	// The probe has to be a real round trip: tmux prints "error creating <socket>"
	// and still exits 0, so a start-server that returns no error has told you
	// nothing at all.
	if out, err := srv.run("new-session", "-d", "-s", "probe", "cat"); err != nil {
		t.Skipf("cannot start a private tmux server here: %v: %s", err, out)
	}
	if out, err := srv.run("has-session", "-t", "=probe"); err != nil {
		t.Skipf("private tmux server is not usable here: %v: %s", err, out)
	}
	if out, err := srv.run("kill-session", "-t", "=probe"); err != nil {
		t.Skipf("private tmux server is not usable here: %v: %s", err, out)
	}

	return srv
}

// session returns a Session pointed at this private server.
func (s *server) session(name string) *Session {
	return &Session{Name: name, Directory: s.t.TempDir(), socket: s.socket}
}

func (s *server) run(args ...string) (string, error) {
	out, err := exec.Command("tmux", append([]string{"-S", s.socket}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (s *server) must(args ...string) string {
	s.t.Helper()
	out, err := s.run(args...)
	if err != nil {
		s.t.Fatalf("tmux %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return out
}

// newSession creates a session directly, bypassing bloom, so a test can set up
// the world it wants to assert against.
func (s *server) newSession(name, window string) {
	s.t.Helper()
	s.must("new-session", "-d", "-s", name, "-n", window, "cat")
}

func (s *server) windowNames(session string) []string {
	s.t.Helper()
	out := s.must("list-windows", "-t", "="+session, "-F", "#{window_name}")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func (s *server) hasSession(name string) bool {
	s.t.Helper()
	_, err := s.run("has-session", "-t", "="+name)
	return err == nil
}

func tools(names ...string) []config.Tool {
	out := make([]config.Tool, 0, len(names))
	for _, name := range names {
		// A command that differs from the window name on purpose: if tmux ever
		// renamed windows after the fact, these tests would catch it.
		out = append(out, config.Tool{Name: name, Command: "cat"})
	}
	return out
}

// The bug this release exists to fix. tmux resolves a target as an exact name,
// then the start of a name, then a glob — so with api_v2 running, a bare
// `has-session -t api` says yes, and bloom attaches to a session for a different
// project instead of creating its own.
func TestExistsDoesNotMatchAPrefixSession(t *testing.T) {
	srv := newServer(t)
	srv.newSession("api_v2", "shell")

	exists, err := srv.session("api").Exists()
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Error("api must not match the running api_v2")
	}

	exists, err = srv.session("api_v2").Exists()
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Error("api_v2 must match itself")
	}
}

// The same bug with the sharpest consequence: `bloom --kill ~/src/api` used to
// take down api_v2 and everything running in it.
func TestKillLeavesAPrefixNamedSessionAlone(t *testing.T) {
	srv := newServer(t)
	srv.newSession("api_v2", "shell")

	killed, err := srv.session("api").Kill()
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if killed {
		t.Error("Kill reported killing a session that was never there")
	}
	if !srv.hasSession("api_v2") {
		t.Fatal("Kill destroyed api_v2, which is data loss")
	}
}

func TestKillReportsWhetherASessionWasThere(t *testing.T) {
	srv := newServer(t)
	srv.newSession("gone", "shell")

	killed, err := srv.session("gone").Kill()
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if !killed {
		t.Error("Kill should report killing a session that was running")
	}
	if srv.hasSession("gone") {
		t.Error("session survived Kill")
	}

	// Asking twice is not an error: the caller wanted it gone and it is gone.
	killed, err = srv.session("gone").Kill()
	if err != nil {
		t.Fatalf("second Kill: %v", err)
	}
	if killed {
		t.Error("the second Kill should report that nothing changed")
	}
}

// Window targets prefix-match too, so focusing "web" would land on "webpack".
func TestSelectWindowDoesNotMatchAPrefixWindow(t *testing.T) {
	srv := newServer(t)
	srv.newSession("proj", "webpack")

	s := srv.session("proj")
	if err := s.SelectWindow("web"); err == nil {
		t.Error(`SelectWindow("web") should not have matched the "webpack" window`)
	}
	if err := s.SelectWindow("webpack"); err != nil {
		t.Errorf(`SelectWindow("webpack") = %v, want it to match exactly`, err)
	}
}

func TestCreateOpensOneWindowPerToolInOrder(t *testing.T) {
	srv := newServer(t)
	s := srv.session("proj")

	if err := s.Create(tools("nvim", "lazygit", "claude")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := srv.windowNames("proj")
	want := []string{"nvim", "lazygit", "claude"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("windows = %v, want %v", got, want)
	}
}

// bloom finds every window by name forever after, so the names it sets have to
// stick. tmux disables automatic-rename for a window named at creation; this
// pins that, because the commands here are all "cat" and would otherwise show up
// under that name.
func TestCreatedWindowNamesAreNotRenamed(t *testing.T) {
	srv := newServer(t)
	s := srv.session("proj")

	if err := s.Create(tools("editor", "assistant")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Long enough for a rename triggered by the running command to have shown up.
	time.Sleep(500 * time.Millisecond)

	got := srv.windowNames("proj")
	if strings.Join(got, ",") != "editor,assistant" {
		t.Errorf("windows = %v, want the configured names to survive", got)
	}
}

func TestWindowsListsTheSessionsOwnWindows(t *testing.T) {
	srv := newServer(t)
	// A prefix-named neighbour, to be sure the listing is not reading from it.
	srv.newSession("proj_other", "decoy")

	s := srv.session("proj")
	if err := s.Create(tools("nvim", "zsh")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.Windows()
	if err != nil {
		t.Fatalf("Windows: %v", err)
	}
	if strings.Join(got, ",") != "nvim,zsh" {
		t.Errorf("Windows = %v, want [nvim zsh]", got)
	}
}

// Adding a tool to the config is the common case, and it must not cost you what
// the other windows were doing.
func TestSyncAddsOnlyWhatIsMissingAndLeavesTheRestRunning(t *testing.T) {
	srv := newServer(t)
	s := srv.session("proj")

	if err := s.Create(tools("nvim", "zsh")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	before := srv.must("list-panes", "-t", "=proj:=nvim", "-F", "#{pane_pid}")

	created, err := s.Sync(tools("nvim", "zsh", "claude", "lazygit"))
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if strings.Join(created, ",") != "claude,lazygit" {
		t.Errorf("Sync created %v, want [claude lazygit]", created)
	}

	after := srv.must("list-panes", "-t", "=proj:=nvim", "-F", "#{pane_pid}")
	if before != after {
		t.Errorf("Sync restarted the nvim window: pane pid %s -> %s", before, after)
	}

	if got := strings.Join(srv.windowNames("proj"), ","); got != "nvim,zsh,claude,lazygit" {
		t.Errorf("windows = %s", got)
	}
}

func TestSyncAddsNothingWhenTheSessionMatches(t *testing.T) {
	srv := newServer(t)
	s := srv.session("proj")

	if err := s.Create(tools("nvim", "zsh")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	created, err := s.Sync(tools("nvim", "zsh"))
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("Sync created %v, want nothing", created)
	}
}

// Refresh is the opposite bargain: it kills what is running so a changed command
// takes effect.
func TestRefreshRespawnsEveryWindow(t *testing.T) {
	srv := newServer(t)
	s := srv.session("proj")

	if err := s.Create(tools("nvim", "zsh")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	before := srv.must("list-panes", "-t", "=proj:=nvim", "-F", "#{pane_pid}")

	if err := s.Refresh(tools("nvim", "zsh"), ""); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	after := srv.must("list-panes", "-t", "=proj:=nvim", "-F", "#{pane_pid}")
	if before == after {
		t.Errorf("Refresh left the nvim window running: pane pid %s unchanged", before)
	}
}

func TestRefreshTouchesOnlyTheNamedWindow(t *testing.T) {
	srv := newServer(t)
	s := srv.session("proj")

	if err := s.Create(tools("nvim", "zsh")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	nvimBefore := srv.must("list-panes", "-t", "=proj:=nvim", "-F", "#{pane_pid}")
	zshBefore := srv.must("list-panes", "-t", "=proj:=zsh", "-F", "#{pane_pid}")

	if err := s.Refresh(tools("nvim", "zsh"), "zsh"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if got := srv.must("list-panes", "-t", "=proj:=nvim", "-F", "#{pane_pid}"); got != nvimBefore {
		t.Errorf("nvim was respawned but only zsh was named: %s -> %s", nvimBefore, got)
	}
	if got := srv.must("list-panes", "-t", "=proj:=zsh", "-F", "#{pane_pid}"); got == zshBefore {
		t.Errorf("zsh was not respawned: pane pid %s unchanged", zshBefore)
	}
}

// A tool added to the config since the session started has no window to respawn,
// and erroring there would make --refresh useless exactly when the config changed.
func TestRefreshCreatesAWindowThatIsMissing(t *testing.T) {
	srv := newServer(t)
	s := srv.session("proj")

	if err := s.Create(tools("nvim")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Refresh(tools("nvim", "claude"), ""); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := strings.Join(srv.windowNames("proj"), ","); got != "nvim,claude" {
		t.Errorf("windows = %s, want nvim,claude", got)
	}
}

func TestRefreshRejectsAnUnknownWindow(t *testing.T) {
	srv := newServer(t)
	s := srv.session("proj")

	if err := s.Create(tools("nvim")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	err := s.Refresh(tools("nvim"), "editor")
	if err == nil {
		t.Fatal("expected an error for a tool that is not configured")
	}
	if !strings.Contains(err.Error(), "editor") {
		t.Errorf("error should name the window asked for, got %q", err)
	}
}

// Send has to land in the window that was asked for. With a prefix-named
// neighbour present, a bare target would deliver the text to the wrong program —
// which for an assistant window means prompting the wrong thing entirely.
func TestSendReachesTheNamedWindowAndNotItsPrefixNeighbour(t *testing.T) {
	srv := newServer(t)
	s := srv.session("proj")

	if err := s.Create([]config.Tool{
		{Name: "webpack", Command: "cat"},
		{Name: "web", Command: "cat"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	const marker = "bloom-send-marker"
	if err := s.Send("web", marker); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if !srv.waitForPane("proj", "web", marker) {
		t.Errorf("%q never reached the web window; got:\n%s", marker, srv.capture("proj", "web"))
	}
	if got := srv.capture("proj", "webpack"); strings.Contains(got, marker) {
		t.Errorf("%q leaked into the webpack window:\n%s", marker, got)
	}
}

func TestSendRejectsAWindowThatIsNotThere(t *testing.T) {
	srv := newServer(t)
	s := srv.session("proj")

	if err := s.Create(tools("nvim")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Send("claude", "hello"); err == nil {
		t.Error("expected Send to a missing window to fail rather than go somewhere else")
	}
}

// capture returns what is on screen in a window.
func (s *server) capture(session, window string) string {
	s.t.Helper()
	out, _ := s.run("capture-pane", "-p", "-t", "="+session+":="+window)
	return out
}

// waitForPane polls, because tmux delivers a paste asynchronously and offers no
// command that blocks until it has landed.
func (s *server) waitForPane(session, window, want string) bool {
	s.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(s.capture(session, window), want) {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

// Exists answers by way of tmux's exit status, so it has to tell "tmux said no"
// apart from "tmux never ran". Reporting a missing binary as "no session" is how
// --kill came to report success having done nothing at all.
func TestExistsReportsAnErrorWhenTmuxCannotRun(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	exists, err := NewSession("whatever", "").Exists()
	if err == nil {
		t.Fatal("expected an error when tmux is not on PATH, got nil")
	}
	if exists {
		t.Error("Exists should not claim a session exists when it could not ask")
	}
}

func TestAvailableFailsWithoutTmux(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := Available()
	if err == nil {
		t.Fatal("expected Available to fail when tmux is not on PATH")
	}
	if !strings.Contains(err.Error(), "tmux") {
		t.Errorf("error should name tmux, got %q", err)
	}
}

func TestCreateRejectsAnEmptyToolList(t *testing.T) {
	if err := NewSession("proj", "/tmp").Create(nil); err == nil {
		t.Error("expected Create with no tools to fail")
	}
}
