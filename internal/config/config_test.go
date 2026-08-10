package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A tool's name is its window name, and bloom addresses windows by name. tmux
// resolves a window target as an index, then an exact name, then a prefix, then
// a glob — so a name that collides with any of that syntax is unreachable, and
// the symptom shows up much later as --send landing somewhere else.
func TestWindowNameProblem(t *testing.T) {
	rejected := map[string]string{
		"web.1":    "'.'",
		"a:b":      "':'",
		"my tool":  "whitespace",
		"my\ttool": "whitespace",
		"log*":     "*?[]",
		"log[1]":   "*?[]",
		"2":        "digits",
		"42":       "digits",
		"=web":     "may not start",
		"@1":       "may not start",
		"$0":       "may not start",
		"{start}":  "may not start",
		"^":        "may not start",
		"!":        "may not start",
		"+":        "may not start",
		"-w":       "may not start",
		" leading": "whitespace",
	}
	for name, want := range rejected {
		t.Run(name, func(t *testing.T) {
			got := windowNameProblem(name)
			if got == "" {
				t.Fatalf("windowNameProblem(%q) = \"\", want a rejection", name)
			}
			if !strings.Contains(got, want) {
				t.Errorf("windowNameProblem(%q) = %q, want it to mention %q", name, got, want)
			}
		})
	}

	accepted := []string{"nvim", "lazygit", "claude", "opencode", "zsh", "web_1", "s3", "_scratch", "Server"}
	for _, name := range accepted {
		t.Run(name, func(t *testing.T) {
			if got := windowNameProblem(name); got != "" {
				t.Errorf("windowNameProblem(%q) = %q, want it accepted", name, got)
			}
		})
	}
}

func TestLoadRejectsUnaddressableNames(t *testing.T) {
	path := write(t, t.TempDir(), ".bloom.yml", `
tools:
  - name: web.1
    command: npm run dev
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected a config with an unaddressable tool name to be rejected")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error should name the config file, got %q", err)
	}
	if !strings.Contains(err.Error(), "web.1") {
		t.Errorf("error should name the offending tool, got %q", err)
	}
}

// Two windows of the same name leave --send and --refresh with no way to say
// which one they meant, and Sync silently skipping the second is not an answer.
func TestLoadRejectsDuplicateNames(t *testing.T) {
	path := write(t, t.TempDir(), ".bloom.yml", `
tools:
  - name: claude
    command: claude
  - name: claude
    command: claude --resume
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected duplicate tool names to be rejected")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should say what is wrong, got %q", err)
	}
}

// One run should be enough to fix a config, rather than one run per mistake.
func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	path := write(t, t.TempDir(), ".bloom.yml", `
select: nope
tools:
  - name: web.1
    command: npm run dev
  - name: 2
    command: sh
  - name: nvim
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected the config to be rejected")
	}
	for _, want := range []string{"web.1", `"2"`, "nvim", "select"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s; got:\n%s", want, err)
		}
	}
}

// A typo'd select used to surface as "could not select window" after the session
// was already built, if it surfaced at all.
func TestLoadRejectsSelectThatNamesNoTool(t *testing.T) {
	path := write(t, t.TempDir(), ".bloom.yml", `
select: editor
tools:
  - name: nvim
    command: nvim
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected select naming no tool to be rejected")
	}
}

// Reaching Create before failing meant the error arrived with no config path
// attached, from a function that only knows it was handed nothing.
func TestLoadRejectsAnEmptyToolList(t *testing.T) {
	path := write(t, t.TempDir(), ".bloom.yml", "select: nvim\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected a config with no tools to be rejected")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error should name the config file, got %q", err)
	}
}

func TestLoadDefaultsSelectToTheFirstTool(t *testing.T) {
	path := write(t, t.TempDir(), ".bloom.yml", `
tools:
  - name: nvim
    command: nvim
  - name: zsh
    command: zsh
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Select != "nvim" {
		t.Errorf("Select = %q, want the first tool %q", cfg.Select, "nvim")
	}
}

// Find is what lets --print promise to change nothing: it has to be able to
// report "no config" without one appearing as a side effect.
func TestFindCreatesNothing(t *testing.T) {
	project := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	if path, found := Find(project); found {
		t.Fatalf("Find found %q in an empty tree", path)
	}

	for _, dir := range []string{project, configHome} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("Find wrote something into %s: %v", dir, entries)
		}
	}
}

func TestFindPrefersTheProjectFile(t *testing.T) {
	project := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	global := filepath.Join(configHome, "bloom", "config.yml")
	if err := os.MkdirAll(filepath.Dir(global), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(global, []byte("tools:\n  - name: zsh\n    command: zsh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	local := write(t, project, ".bloom.yml", "tools:\n  - name: nvim\n    command: nvim\n")

	path, found := Find(project)
	if !found {
		t.Fatal("expected to find a config")
	}
	if path != local {
		t.Errorf("Find = %q, want the project file %q", path, local)
	}
}

func TestFindFallsBackToTheGlobalFile(t *testing.T) {
	project := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	global := filepath.Join(configHome, "bloom", "config.yml")
	if err := os.MkdirAll(filepath.Dir(global), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(global, []byte("tools:\n  - name: zsh\n    command: zsh\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	path, found := Find(project)
	if !found || path != global {
		t.Errorf("Find = (%q, %v), want (%q, true)", path, found, global)
	}
}

func TestWriteDefaultIsLoadable(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	path, err := WriteDefault("zsh")
	if err != nil {
		t.Fatalf("WriteDefault: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("the config bloom writes itself must load: %v", err)
	}
	if cfg.Select != "zsh" || len(cfg.Tools) != 1 || cfg.Tools[0].Command != "zsh" {
		t.Errorf("round-tripped default = %+v", cfg)
	}
}

// The shell name reaches a window name, so a login shell bloom cannot address
// would produce a default config that fails to load.
func TestDefaultUsesTheShellForBothFields(t *testing.T) {
	cfg := Default("fish")
	if cfg.Select != "fish" {
		t.Errorf("Select = %q, want %q", cfg.Select, "fish")
	}
	if len(cfg.Tools) != 1 || cfg.Tools[0].Name != "fish" || cfg.Tools[0].Command != "fish" {
		t.Errorf("Tools = %+v", cfg.Tools)
	}
	if problem := windowNameProblem(cfg.Tools[0].Name); problem != "" {
		t.Errorf("the default config must be addressable: %s", problem)
	}
}
