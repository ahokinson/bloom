package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ahokinson/bloom/internal/config"
	"github.com/ahokinson/bloom/internal/session"
	"github.com/ahokinson/bloom/internal/shell"
)

func main() {
	projectDirectory := "."
	if len(os.Args) > 1 {
		projectDirectory = os.Args[1]
	}

	absoluteDirectory, err := filepath.Abs(projectDirectory)
	if err != nil {
		fatal("resolving directory: %v", err)
	}

	info, err := os.Stat(absoluteDirectory)
	if err != nil || !info.IsDir() {
		fatal("directory '%s' does not exist", projectDirectory)
	}

	defaultShell := shell.Default()

	cfg, err := config.Load(absoluteDirectory, defaultShell)
	if err != nil {
		fatal("%v", err)
	}
	name := session.SanitizeName(filepath.Base(absoluteDirectory))
	s := session.NewSession(name, absoluteDirectory)

	if !s.Exists() {
		if err := s.Create(cfg.Tools); err != nil {
			fatal("creating session: %v", err)
		}
	}

	if err := s.SelectWindow(cfg.Select); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not select window %q: %v\n", cfg.Select, err)
	}
	if err := s.Attach(); err != nil {
		fatal("attaching to session: %v", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}
