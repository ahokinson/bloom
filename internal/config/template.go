package config

import (
	"fmt"
	"strings"
	"text/template"
)

// Vars are the values a tool command may interpolate.
//
// File is empty when none was given, which is what makes the whole scheme
// degrade cleanly: a command written as
//
//	claude{{if .File}} "$(cat {{.File}})"{{end}}
//
// is just "claude" on an ordinary run and needs no second configuration path
// for the case where no file was passed.
//
// Only the path is exposed, never the file's contents. bloom does not know what
// is in the file, and splicing arbitrary text into a shell command is how you
// get a window that fails to start — or one that runs what the file happened to
// contain.
type Vars struct {
	// Dir is the absolute project directory.
	Dir string
	// Name is the resolved session name.
	Name string
	// File is the absolute path passed with --file, empty when unset.
	File string
}

// expand runs one command through text/template. An unknown field is an error
// rather than an empty string: a typo that silently deletes half a command is
// worse than a startup failure that says which field is wrong.
func expand(command string, v Vars) (string, error) {
	t, err := template.New("command").Option("missingkey=error").Parse(command)
	if err != nil {
		return "", fmt.Errorf("parsing command template %q: %w", command, err)
	}
	var out strings.Builder
	if err := t.Execute(&out, v); err != nil {
		return "", fmt.Errorf("expanding command template %q: %w", command, err)
	}
	return out.String(), nil
}

// Render expands every tool's command.
//
// A command containing no action is returned byte-identical without going near
// the template engine. Most commands are a bare `nvim` or `lazygit`, and those
// should not depend on template syntax they never asked for: no parse step means
// no way for one to fail, and nothing in a shell one-liner can be mistaken for
// an action.
func Render(tools []Tool, v Vars) ([]Tool, error) {
	out := make([]Tool, len(tools))
	for i, tool := range tools {
		if !strings.Contains(tool.Command, "{{") {
			out[i] = tool
			continue
		}
		expanded, err := expand(tool.Command, v)
		if err != nil {
			return nil, fmt.Errorf("tool %q: %w", tool.Name, err)
		}
		out[i] = Tool{Name: tool.Name, Command: expanded}
	}
	return out, nil
}
