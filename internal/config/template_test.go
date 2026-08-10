package config

import (
	"strings"
	"testing"
)

func TestRenderLeavesPlainCommandsUntouched(t *testing.T) {
	// Most commands are a bare binary or a shell one-liner, and none of them
	// asked to be templated. Skipping the engine entirely is what guarantees
	// there is no parse step for them to trip over — note the quoted `$HOME` and
	// the pipe below, which a naive expansion would be free to mangle.
	tools := []Tool{
		{Name: "zsh", Command: "zsh"},
		{Name: "nvim", Command: "nvim"},
		{Name: "lazygit", Command: "lazygit"},
		{Name: "claude", Command: "claude"},
		{Name: "quoted", Command: `sh -c "echo $HOME && ls -la | grep x"`},
	}
	got, err := Render(tools, Vars{Dir: "/tmp/x", Name: "x", File: "/tmp/x/notes.md"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for i, tool := range tools {
		if got[i] != tool {
			t.Errorf("tool %d: got %+v, want byte-identical %+v", i, got[i], tool)
		}
	}
}

func TestRenderWithFile(t *testing.T) {
	tools := []Tool{
		{Name: "claude", Command: `claude{{if .File}} "$(cat {{.File}})"{{end}}`},
	}
	got, err := Render(tools, Vars{File: "/tmp/notes.md"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := `claude "$(cat /tmp/notes.md)"`
	if got[0].Command != want {
		t.Errorf("got %q, want %q", got[0].Command, want)
	}
}

func TestRenderWithoutFileCollapsesToBareCommand(t *testing.T) {
	tools := []Tool{
		{Name: "claude", Command: `claude{{if .File}} "$(cat {{.File}})"{{end}}`},
	}
	got, err := Render(tools, Vars{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got[0].Command != "claude" {
		t.Errorf("got %q, want %q", got[0].Command, "claude")
	}
}

func TestExpandDirAndName(t *testing.T) {
	v := Vars{Dir: "/home/u/src/my-project", Name: "my_project"}
	cases := map[string]string{
		"nvim {{.Dir}}":                  "nvim /home/u/src/my-project",
		"echo {{.Name}}":                 "echo my_project",
		"sh -c 'cd {{.Dir}} && lazygit'": "sh -c 'cd /home/u/src/my-project && lazygit'",
	}
	for in, want := range cases {
		got, err := expand(in, v)
		if err != nil {
			t.Fatalf("expand(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("expand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExpandUnknownFieldIsAnError(t *testing.T) {
	// A silent empty string here would delete part of a command and leave the
	// user staring at a window that exited instantly.
	if _, err := expand("claude {{.Nonsense}}", Vars{}); err == nil {
		t.Fatal("expected an error for an unknown field, got nil")
	}
}

func TestExpandMalformedTemplateIsAnError(t *testing.T) {
	if _, err := expand("claude {{.File", Vars{}); err == nil {
		t.Fatal("expected an error for an unterminated action, got nil")
	}
}

func TestRenderNamesTheFailingTool(t *testing.T) {
	tools := []Tool{
		{Name: "zsh", Command: "zsh"},
		{Name: "broken", Command: "{{.Nope}}"},
	}
	_, err := Render(tools, Vars{})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("error should name the offending tool, got %q", err)
	}
}
