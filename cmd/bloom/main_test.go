package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahokinson/bloom/internal/config"
)

// The flag set is the contract callers script against, so it is pinned here
// rather than left to be discovered by a broken invocation.
func TestParseArgs(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want options
	}{
		{
			name: "bare invocation defaults to the current directory",
			argv: nil,
			want: options{dir: "."},
		},
		{
			name: "positional directory",
			argv: []string{"/src/x"},
			want: options{dir: "/src/x"},
		},
		{
			name: "flags before the positional",
			argv: []string{"--session", "s", "--file", "/n.md", "--window", "w", "--refresh", "/src/x"},
			want: options{dir: "/src/x", session: "s", file: "/n.md", window: "w", refresh: true},
		},
		{
			name: "single-dash spellings",
			argv: []string{"-session", "s", "-no-attach", "-print"},
			want: options{dir: ".", session: "s", noAttach: true, dryRun: true},
		},
		{
			name: "version",
			argv: []string{"--version"},
			want: options{dir: ".", version: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseArgs(tc.argv)
			if err != nil {
				t.Fatalf("parseArgs(%q): %v", tc.argv, err)
			}
			if got != tc.want {
				t.Errorf("parseArgs(%q) = %+v, want %+v", tc.argv, got, tc.want)
			}
		})
	}
}

// Go's flag package stops at the first positional, so a flag written after the
// directory is not applied. It used to be dropped in silence, which is the worst
// way to learn that --refresh did nothing; now it is an error.
func TestParseArgsRejectsFlagsAfterTheDirectory(t *testing.T) {
	_, err := parseArgs([]string{"/src/x", "--refresh"})
	if err == nil {
		t.Fatal("expected --refresh after the directory to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "--refresh") {
		t.Errorf("error should name the argument it could not use, got %q", err)
	}
}

func TestParseArgsRejectsASecondDirectory(t *testing.T) {
	if _, err := parseArgs([]string{"/src/x", "/src/y"}); err == nil {
		t.Fatal("expected a second directory to be rejected, got nil")
	}
}

func TestParseArgsRejectsUnknownFlags(t *testing.T) {
	if _, err := parseArgs([]string{"--prompt-file", "/n.md"}); err == nil {
		t.Fatal("expected an error for an unknown flag, got nil")
	}
}

// An empty --send is a real instruction; not passing --send at all is not. The
// flag's value cannot tell those apart, so parseArgs records that it was set.
func TestParseArgsDistinguishesEmptySendFromAbsent(t *testing.T) {
	absent, err := parseArgs([]string{"."})
	if err != nil {
		t.Fatal(err)
	}
	if absent.sendSet {
		t.Error("sendSet should be false when --send was not passed")
	}

	empty, err := parseArgs([]string{"--send", "", "."})
	if err != nil {
		t.Fatal(err)
	}
	if !empty.sendSet {
		t.Error("sendSet should be true when --send was passed an empty string")
	}
}

func TestValidateRejectsConflictingModes(t *testing.T) {
	conflicts := map[string]options{
		"refresh and sync": {refresh: true, sync: true},
		"kill and send":    {kill: true, sendSet: true},
		"kill and resolve": {kill: true, resolve: true},
		"send and resolve": {sendSet: true, resolve: true},
		"kill and refresh": {kill: true, refresh: true},
		"kill and print":   {kill: true, dryRun: true},
		"send and sync":    {sendSet: true, sync: true},
	}
	for name, opt := range conflicts {
		t.Run(name, func(t *testing.T) {
			if err := validate(opt); err == nil {
				t.Errorf("expected %+v to be rejected", opt)
			}
		})
	}
}

// --kill, --send and --resolve all answer and exit before a session is built, so
// a flag about building one never runs. Each of these used to be accepted and
// then quietly dropped, which is the behaviour validate exists to prevent — but
// only --kill was actually checked.
func TestValidateRejectsFlagsTheEarlyModesWouldDrop(t *testing.T) {
	dropped := map[string]options{
		"resolve and refresh": {resolve: true, refresh: true},
		"resolve and sync":    {resolve: true, sync: true},
		"resolve and print":   {resolve: true, dryRun: true},
		"resolve and file":    {resolve: true, file: "/n.md"},
		"resolve and window":  {resolve: true, window: "claude"},
		"send and print":      {sendSet: true, dryRun: true},
		"send and file":       {sendSet: true, file: "/n.md"},
		"kill and sync":       {kill: true, sync: true},
		"kill and file":       {kill: true, file: "/n.md"},
		"kill and window":     {kill: true, window: "claude"},
		// --print shows the same plan whether or not a respawn was asked for.
		"print and refresh": {dryRun: true, refresh: true},
		"print and sync":    {dryRun: true, sync: true},
	}
	for name, opt := range dropped {
		t.Run(name, func(t *testing.T) {
			if err := validate(opt); err == nil {
				t.Errorf("expected %+v to be rejected", opt)
			}
		})
	}
}

func TestValidateAllowsUsefulCombinations(t *testing.T) {
	ok := map[string]options{
		"plain":          {},
		"sync and json":  {sync: true, json: true},
		"print and json": {dryRun: true, json: true},
		"kill and json":  {kill: true, json: true},
		"send to window": {sendSet: true, window: "claude"},
		"refresh window": {refresh: true, window: "claude"},
		"resolve json":   {resolve: true, json: true},
	}
	for name, opt := range ok {
		t.Run(name, func(t *testing.T) {
			if err := validate(opt); err != nil {
				t.Errorf("expected %+v to be allowed, got %v", opt, err)
			}
		})
	}
}

// --resolve and --kill have to answer for a directory that no longer exists;
// that is precisely when a caller is cleaning up and still needs the name.
func TestResolveNameIsLexical(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "deleted", "my-project")
	got, err := resolveName(options{dir: gone})
	if err != nil {
		t.Fatalf("resolveName: %v", err)
	}
	if got != "my_project" {
		t.Errorf("resolveName(%q) = %q, want %q", gone, got, "my_project")
	}
}

func TestResolveNamePrefersSession(t *testing.T) {
	got, err := resolveName(options{dir: "/src/other", session: "TICKET-1_repo"})
	if err != nil {
		t.Fatalf("resolveName: %v", err)
	}
	if got != "TICKET_1_repo" {
		t.Errorf("got %q, want %q", got, "TICKET_1_repo")
	}
}

func TestSendTextPassesThroughUnlessDash(t *testing.T) {
	got, err := sendText("hello world")
	if err != nil {
		t.Fatalf("sendText: %v", err)
	}
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestActionWordReportsWhatHappened(t *testing.T) {
	if got := actionWord(true, "killed"); got != "killed" {
		t.Errorf("got %q, want %q", got, "killed")
	}
	// Asking to kill a session that was not there changed nothing, and a caller
	// diffing state should not be told otherwise.
	if got := actionWord(false, "killed"); got != "none" {
		t.Errorf("got %q, want %q", got, "none")
	}
}

func TestResolveFile(t *testing.T) {
	if got, err := resolveFile(""); err != nil || got != "" {
		t.Errorf(`resolveFile("") = %q, %v; want "", nil`, got, err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveFile(path)
	if err != nil {
		t.Fatalf("resolveFile(%q): %v", path, err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("resolveFile(%q) = %q, want an absolute path", path, got)
	}

	// A missing file has to fail here, before any tmux session is created.
	// Otherwise the window starts, the command cannot read its file, and the
	// user is left reading a shell prompt where a tool should be.
	if _, err := resolveFile(filepath.Join(dir, "nope.md")); err == nil {
		t.Fatal("expected an error for a missing file, got nil")
	}

	// Same reasoning: `--context <a directory>` fails inside the window, where
	// there is nobody to read the error.
	if _, err := resolveFile(dir); err == nil {
		t.Fatal("expected an error for a directory, got nil")
	}
}

// --send - with nothing redirected would otherwise block on an idle terminal
// with no output at all, which reads as a hang rather than a mistake.
func TestSendTextRejectsATerminalOnStdin(t *testing.T) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		t.Skip("no controlling terminal available")
	}
	defer tty.Close()

	saved := os.Stdin
	os.Stdin = tty
	defer func() { os.Stdin = saved }()

	if _, err := sendText("-"); err == nil {
		t.Fatal("expected --send - to be rejected when stdin is a terminal")
	}
}

func TestSendTextReadsStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = w.WriteString("from a pipe\n")
		_ = w.Close()
	}()

	saved := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = saved }()

	got, err := sendText("-")
	if err != nil {
		t.Fatalf("sendText: %v", err)
	}
	// sendText hands back what it read; trimming the newline is Send's job,
	// because a caller passing text directly deserves the same treatment.
	if got != "from a pipe\n" {
		t.Errorf("got %q, want %q", got, "from a pipe\n")
	}
}

func TestToolNamesAndNamed(t *testing.T) {
	tools := []config.Tool{{Name: "nvim", Command: "nvim"}, {Name: "claude", Command: "claude"}}
	if !named(tools, "claude") {
		t.Error("named should find a configured tool")
	}
	if named(tools, "claud") {
		t.Error("named must not match on a prefix; that is the bug this release fixes")
	}
	if got := strings.Join(toolNames(tools), ","); got != "nvim,claude" {
		t.Errorf("toolNames = %q, want %q", got, "nvim,claude")
	}
}

func TestOptionalRendersEmptyAsNil(t *testing.T) {
	if optional("") != nil {
		t.Error(`optional("") should be nil so JSON says null`)
	}
	if got := optional("/n.md"); got == nil || *got != "/n.md" {
		t.Errorf("optional(%q) = %v, want a pointer to it", "/n.md", got)
	}
}
