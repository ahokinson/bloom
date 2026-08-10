package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Tool defines a named tmux window and the command it runs.
type Tool struct {
	Name    string `yaml:"name"`
	Command string `yaml:"command"`
}

// Config holds the session layout: which window to select and which tools to open.
type Config struct {
	Select string `yaml:"select"`
	Tools  []Tool `yaml:"tools"`
}

// Find locates the config for a project directory, preferring a project-local
// file over the global one. It creates nothing and reports nothing missing.
//
// Separate from Load so that callers who are only asking a question — --print
// promises to change nothing — can find out there is no config without one
// being written as a side effect.
func Find(projectDirectory string) (string, bool) {
	project := filepath.Join(projectDirectory, ".bloom.yml")
	if fileExists(project) {
		return project, true
	}

	global := DefaultPath()
	if global != "" && fileExists(global) {
		return global, true
	}

	return "", false
}

// Load reads and validates the config at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config %s:\n%w", path, err)
	}

	// Nothing named a window to focus, so the first tool gets it.
	if cfg.Select == "" {
		cfg.Select = cfg.Tools[0].Name
	}

	return &cfg, nil
}

// Default is the config bloom uses when there is none on disk: one window
// running your shell.
func Default(shell string) *Config {
	return &Config{
		Select: shell,
		Tools:  []Tool{{Name: shell, Command: shell}},
	}
}

// WriteDefault writes Default to DefaultPath and returns where it landed. This
// is the only function in the package that writes anything.
func WriteDefault(shell string) (string, error) {
	path := DefaultPath()
	if path == "" {
		return "", errors.New("could not determine a config directory: set XDG_CONFIG_HOME or HOME")
	}

	data, err := yaml.Marshal(Default(shell))
	if err != nil {
		return "", fmt.Errorf("marshaling default config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("creating config directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("writing default config: %w", err)
	}

	return path, nil
}

// DefaultPath is the global config location, honouring XDG_CONFIG_HOME. Empty
// when neither that nor a home directory can be determined.
func DefaultPath() string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "bloom", "config.yml")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// validate reports every problem in one go rather than making the user fix a
// config one error per run.
func (c *Config) validate() error {
	if len(c.Tools) == 0 {
		return errors.New(`  no tools configured; add at least one entry under "tools"`)
	}

	var problems []string
	seen := make(map[string]int, len(c.Tools))

	for i, tool := range c.Tools {
		if tool.Name == "" {
			problems = append(problems, fmt.Sprintf("  tool at index %d: missing a name", i))
			continue
		}
		if tool.Command == "" {
			problems = append(problems, fmt.Sprintf("  tool %q: missing a command", tool.Name))
		}
		if reason := windowNameProblem(tool.Name); reason != "" {
			problems = append(problems, fmt.Sprintf("  tool %q: %s", tool.Name, reason))
		}
		if first, dup := seen[tool.Name]; dup {
			problems = append(problems, fmt.Sprintf(
				"  tool %q: duplicate name, already defined at index %d\n"+
					"    (tmux cannot tell two windows of the same name apart, so --send and --refresh would be ambiguous)",
				tool.Name, first))
			continue
		}
		seen[tool.Name] = i
	}

	// A typo here used to surface as "could not select window" long after the
	// session was built, if it surfaced at all.
	if c.Select != "" {
		if _, ok := seen[c.Select]; !ok {
			problems = append(problems, fmt.Sprintf("  select: %q is not one of the configured tools", c.Select))
		}
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

var (
	allDigits       = regexp.MustCompile(`^[0-9]+$`)
	windowNameStart = regexp.MustCompile(`^[a-zA-Z0-9_]`)
)

// windowNameProblem explains why tmux could not address a window by this name,
// or returns "" when it can.
//
// A tool's name is how bloom finds its window again: --send, --refresh and
// --sync all target by name. tmux resolves a window target as an index, then an
// exact name, then a name prefix, then a glob — and splits session:window.pane
// on the punctuation before any of that. A name that collides with the syntax is
// not addressable, so it is rejected here instead of misbehaving later, when the
// symptom is a prompt appearing in the wrong window.
func windowNameProblem(name string) string {
	switch {
	case strings.Contains(name, ":"):
		return "window name may not contain ':' (tmux reads it as the session:window separator)"
	case strings.Contains(name, "."):
		return `window name may not contain '.' (tmux reads sess:a.b as window "a", pane "b", so --send would miss)`
	case strings.ContainsAny(name, " \t\n\r"):
		return "window name may not contain whitespace"
	case strings.ContainsAny(name, "*?[]"):
		return "window name may not contain any of *?[] (tmux matches those as a glob pattern)"
	case allDigits.MatchString(name):
		return "window name may not be all digits (tmux reads it as a window index)"
	case !windowNameStart.MatchString(name):
		return fmt.Sprintf("window name may not start with %q (tmux reads a leading %q as a target token, not a name)",
			name[:1], name[:1])
	}
	return ""
}
