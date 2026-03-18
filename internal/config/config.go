package config

import (
	"fmt"
	"os"
	"path/filepath"

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

// Load reads the bloom configuration for the given project directory.
// If no config file exists, a default is created at the global config path.
func Load(projectDirectory, shell string) (*Config, error) {
	path := resolveConfigPath(projectDirectory)
	if path == "" {
		var err error
		path, err = createDefault(shell)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(os.Stderr, "Created default config: %s\n", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	var warnings []string
	cfg.Tools, warnings = validTools(cfg.Tools)
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, w)
	}

	if cfg.Select == "" && len(cfg.Tools) > 0 {
		cfg.Select = cfg.Tools[0].Name
	}

	return &cfg, nil
}

func createDefault(shell string) (string, error) {
	path := globalConfigPath()
	if path == "" {
		return "", fmt.Errorf("could not determine config directory")
	}

	cfg := Config{
		Select: shell,
		Tools: []Tool{
			{Name: shell, Command: shell},
		},
	}

	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return "", fmt.Errorf("marshaling default config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("creating config directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("writing default config: %w", err)
	}

	return path, nil
}

func resolveConfigPath(projectDirectory string) string {
	project := filepath.Join(projectDirectory, ".bloom.yml")
	if fileExists(project) {
		return project
	}

	global := globalConfigPath()
	if global != "" && fileExists(global) {
		return global
	}

	return ""
}

func globalConfigPath() string {
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

func validTools(tools []Tool) ([]Tool, []string) {
	var result []Tool
	var warnings []string
	for i, tool := range tools {
		if tool.Name == "" || tool.Command == "" {
			warnings = append(warnings, fmt.Sprintf("Warning: invalid tool configuration at index %d — skipping", i))
			continue
		}
		result = append(result, tool)
	}
	return result, warnings
}
