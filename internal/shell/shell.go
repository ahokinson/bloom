package shell

import (
	"os"
	"path/filepath"
)

// Default returns the basename of the user's login shell, or "sh" as a fallback.
func Default() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return "sh"
	}
	return filepath.Base(shell)
}
