package session

import "regexp"

var sanitize = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// SanitizeName replaces non-alphanumeric characters with underscores for tmux compatibility.
func SanitizeName(raw string) string {
	return sanitize.ReplaceAllString(raw, "_")
}
