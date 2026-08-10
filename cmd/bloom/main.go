package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ahokinson/bloom/internal/config"
	"github.com/ahokinson/bloom/internal/session"
	"github.com/ahokinson/bloom/internal/shell"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `bloom — a tmux session per project directory

Usage:
  bloom [flags] [directory]

Flags:
  --session NAME   Session name (default: the directory's basename)
  --file PATH      A file to expose to tool commands as {{.File}}
  --window NAME    Window to focus, and the one --refresh and --send target
  --refresh        Respawn windows in a session that already exists
  --sync           Add windows the session is missing; leave running ones alone
  --send TEXT      Paste text into a window and submit it ("-" reads stdin)
  --kill           Tear the session down
  --resolve        Print the resolved session name and exit; touches nothing
  --no-attach      Set the session up but stay where you are
  --print          Show the resolved session, windows and commands; change nothing
  --json           Emit the result as JSON. Implies --no-attach.
  --version        Print the version
  -h, --help       Show this help

Templating:
  A tool command may interpolate {{.Dir}}, {{.Name}} and {{.File}}. Commands
  containing no {{ are passed through untouched.

  {{.File}} is empty when --file was not given, so a command written as

      command: some-tool{{if .File}} --context {{.File}}{{end}}

  is plain "some-tool" on an ordinary run and needs no second config entry.

  Only the path is interpolated, never the file's contents.

Naming:
  A tool's name becomes its window name, and bloom addresses windows by name, so
  it has to be something tmux can resolve unambiguously: no "." or ":", no
  whitespace, no glob characters, not all digits, and it must start with a letter,
  digit or underscore. Names are checked when the config is read.

Automation:
  --resolve and --kill work without the directory existing and without reading
  any config, so a session outlives its checkout only as long as you want it to.

  --json reports what happened: one of none, created, refreshed, synced, sent
  or killed. Every JSON form carries "session" and "action"; "exists" reports
  whether the session was already running before bloom touched it.
`

type options struct {
	dir      string
	session  string
	file     string
	window   string
	send     string
	sendSet  bool
	refresh  bool
	sync     bool
	kill     bool
	resolve  bool
	noAttach bool
	dryRun   bool
	json     bool
	version  bool
}

func parseArgs(argv []string) (options, error) {
	var opt options
	fs := flag.NewFlagSet("bloom", flag.ContinueOnError)
	// Everything a caller sees comes from main, in one voice: flag's own output
	// would otherwise name a bad flag first and get the "Error:" prefix second.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	fs.StringVar(&opt.session, "session", "", "session name")
	fs.StringVar(&opt.file, "file", "", "file to expose as {{.File}}")
	fs.StringVar(&opt.window, "window", "", "window to focus, refresh and send to")
	fs.StringVar(&opt.send, "send", "", "text to paste into the window")
	fs.BoolVar(&opt.refresh, "refresh", false, "respawn windows")
	fs.BoolVar(&opt.sync, "sync", false, "add missing windows")
	fs.BoolVar(&opt.kill, "kill", false, "tear the session down")
	fs.BoolVar(&opt.resolve, "resolve", false, "print the resolved session name")
	fs.BoolVar(&opt.noAttach, "no-attach", false, "do not attach")
	fs.BoolVar(&opt.dryRun, "print", false, "print the resolved plan and exit")
	fs.BoolVar(&opt.json, "json", false, "emit the result as JSON")
	fs.BoolVar(&opt.version, "version", false, "print the version")

	if err := fs.Parse(argv); err != nil {
		return opt, err
	}

	// An empty --send is a real request to send nothing, which is different from
	// not passing it at all. Only the flag set knows which happened.
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "send" {
			opt.sendSet = true
		}
	})

	// Go's flag parsing stops at the first positional, so a second one is almost
	// always a flag written after the directory and silently dropped.
	if fs.NArg() > 1 {
		return opt, fmt.Errorf("unexpected argument %q: bloom takes at most one directory, and flags must come before it", fs.Arg(1))
	}

	opt.dir = "."
	if fs.NArg() > 0 {
		opt.dir = fs.Arg(0)
	}
	return opt, nil
}

// requested reports which of the named flags were actually asked for.
func requested(opt options, names ...string) []string {
	on := map[string]bool{
		"--refresh": opt.refresh,
		"--sync":    opt.sync,
		"--print":   opt.dryRun,
		"--file":    opt.file != "",
		"--window":  opt.window != "",
	}
	var out []string
	for _, name := range names {
		if on[name] {
			out = append(out, name)
		}
	}
	return out
}

// validate rejects flag combinations that would otherwise have to pick a winner
// silently. Guessing at intent is how a caller ends up killing a session it
// meant to refresh.
func validate(opt options) error {
	if opt.refresh && opt.sync {
		return errors.New("--refresh and --sync ask for opposite things: one respawns windows, the other leaves them running")
	}

	// These three answer and exit before a session is ever built, so any flag
	// about building one is dead on arrival. Each names the flags it would drop.
	modes := []struct {
		on      bool
		name    string
		ignores []string
	}{
		{opt.kill, "--kill", []string{"--refresh", "--sync", "--print", "--file", "--window"}},
		{opt.sendSet, "--send", []string{"--refresh", "--sync", "--print", "--file"}},
		{opt.resolve, "--resolve", []string{"--refresh", "--sync", "--print", "--file", "--window"}},
	}

	var on []string
	for _, mode := range modes {
		if mode.on {
			on = append(on, mode.name)
		}
	}
	if len(on) > 1 {
		return fmt.Errorf("%s cannot be combined", strings.Join(on, " and "))
	}

	for _, mode := range modes {
		if !mode.on {
			continue
		}
		if dropped := requested(opt, mode.ignores...); len(dropped) > 0 {
			return fmt.Errorf("%s cannot be combined with %s: %s would be ignored",
				mode.name, strings.Join(dropped, " or "), plural(dropped))
		}
	}

	// --print changes nothing and shows the same plan either way, so taking these
	// would imply the preview had accounted for them.
	if opt.dryRun {
		if dropped := requested(opt, "--refresh", "--sync"); len(dropped) > 0 {
			return fmt.Errorf("--print cannot be combined with %s: %s would be ignored",
				strings.Join(dropped, " or "), plural(dropped))
		}
	}

	return nil
}

func plural(names []string) string {
	if len(names) == 1 {
		return "it"
	}
	return "they"
}

// resolveName derives the session name without touching the filesystem.
//
// Lexical on purpose: --resolve and --kill have to work for a directory that
// has already been deleted, which is exactly when a caller is cleaning up after
// itself and still needs to know what the session was called.
func resolveName(opt options) (string, error) {
	if opt.session != "" {
		return session.SanitizeName(opt.session), nil
	}
	abs, err := filepath.Abs(opt.dir)
	if err != nil {
		return "", fmt.Errorf("resolving directory: %w", err)
	}
	return session.SanitizeName(filepath.Base(abs)), nil
}

// resolveFile returns the absolute path exposed as {{.File}}, or "" when --file
// was not given.
func resolveFile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving --file: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("--file %s: %w", abs, err)
	}
	// Commands are written expecting something readable — `--context {{.File}}`
	// against a directory fails inside the window, where nobody is watching.
	if info.IsDir() {
		return "", fmt.Errorf("--file %s is a directory, not a file", abs)
	}
	return abs, nil
}

// sendText returns the text to paste, reading stdin for "-".
//
// Reading stdin is the safer path for anything long or multi-line: it never
// goes near a shell, so quoting cannot mangle it and a `$(...)` inside it stays
// literal text.
func sendText(arg string) (string, error) {
	if arg != "-" {
		return arg, nil
	}
	// A terminal on stdin means nothing was redirected, and reading it would
	// block forever with nothing on screen to say why.
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", fmt.Errorf("inspecting stdin: %w", err)
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", errors.New("--send - expects text on stdin (e.g. bloom --send - --window claude < notes.md)")
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("reading --send from stdin: %w", err)
	}
	return string(b), nil
}

// loadConfig resolves the config for a directory, writing a default only when
// bloom is actually going to build something. --print promises to change
// nothing, so it gets the same default in memory and an empty path to say so.
func loadConfig(directory string, dryRun bool) (*config.Config, string, error) {
	if path, found := config.Find(directory); found {
		cfg, err := config.Load(path)
		if err != nil {
			return nil, "", err
		}
		return cfg, path, nil
	}

	if dryRun {
		return config.Default(shell.Default()), "", nil
	}

	path, err := config.WriteDefault(shell.Default())
	if err != nil {
		return nil, "", err
	}
	warn("no config found; created a default at %s", path)

	cfg, err := config.Load(path)
	if err != nil {
		return nil, "", err
	}
	return cfg, path, nil
}

// plan is everything a run resolved to, and the single source for --print and
// --json alike.
type plan struct {
	session   string
	directory string
	config    string // "" when no config file exists
	file      string
	selected  string
	tools     []config.Tool
	existed   bool
	action    string
}

type jsonWindow struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Live    bool   `json:"live"`
}

type jsonPlan struct {
	Session   string       `json:"session"`
	Action    string       `json:"action"`
	Directory string       `json:"directory"`
	Config    *string      `json:"config"`
	File      *string      `json:"file"`
	Exists    bool         `json:"exists"`
	Select    string       `json:"select"`
	Windows   []jsonWindow `json:"windows"`
}

// jsonAction is what the modes that do not build a session report. Every JSON
// form shares these two fields so a consumer can read the outcome without
// knowing which flag produced it.
type jsonAction struct {
	Session string `json:"session"`
	Action  string `json:"action"`
}

func emit(v any) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fatal("encoding json: %v", err)
	}
	fmt.Println(string(out))
}

func main() {
	opt, err := parseArgs(os.Args[1:])
	if err != nil {
		// Asking for help is not an error, and a caller probing for a flag
		// needs a clean exit to tell "unsupported" from "unavailable".
		if errors.Is(err, flag.ErrHelp) {
			fmt.Print(usage)
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if opt.version {
		fmt.Println(version)
		return
	}
	if err := validate(opt); err != nil {
		fatal("%v", err)
	}
	// Emitting JSON and then handing the terminal to tmux cannot both happen.
	if opt.json {
		opt.noAttach = true
	}

	name, err := resolveName(opt)
	if err != nil {
		fatal("%v", err)
	}

	// --resolve is a question about naming. It reads no config, stats no
	// directory and does not even need tmux installed, which is what makes it
	// safe to call from a script that is about to create the checkout.
	if opt.resolve {
		if opt.json {
			emit(jsonAction{Session: name, Action: "none"})
		} else {
			fmt.Println(name)
		}
		return
	}

	// Everything past here shells out to tmux. Asking once means a missing
	// binary is reported as such, rather than as an empty answer from a check
	// three layers down.
	if err := session.Available(); err != nil {
		fatal("%v", err)
	}

	if opt.kill {
		s := session.NewSession(name, "")
		killed, err := s.Kill()
		if err != nil {
			fatal("%v", err)
		}
		report(opt, name, actionWord(killed, "killed"))
		return
	}

	if opt.sendSet {
		s := session.NewSession(name, "")
		exists, err := s.Exists()
		if err != nil {
			fatal("%v", err)
		}
		if !exists {
			fatal("no session named %s to send to", name)
		}
		text, err := sendText(opt.send)
		if err != nil {
			fatal("%v", err)
		}
		if err := s.Send(opt.window, text); err != nil {
			fatal("%v", err)
		}
		report(opt, name, "sent")
		return
	}

	absoluteDirectory, err := filepath.Abs(opt.dir)
	if err != nil {
		fatal("resolving directory: %v", err)
	}

	info, err := os.Stat(absoluteDirectory)
	if err != nil || !info.IsDir() {
		fatal("directory '%s' does not exist", opt.dir)
	}

	cfg, configPath, err := loadConfig(absoluteDirectory, opt.dryRun)
	if err != nil {
		fatal("%v", err)
	}

	file, err := resolveFile(opt.file)
	if err != nil {
		fatal("%v", err)
	}

	tools, err := config.Render(cfg.Tools, config.Vars{
		Dir:  absoluteDirectory,
		Name: name,
		File: file,
	})
	if err != nil {
		fatal("%v", err)
	}

	// One resolved window, used for focus, --refresh and --print alike. Left
	// empty, --refresh means every window rather than an arbitrary one.
	window := opt.window
	if window == "" {
		window = cfg.Select
	}
	// cfg.Select is checked when the config loads; --window is checked here, so
	// a typo is a sentence rather than a session with the wrong thing in front.
	if opt.window != "" && !named(tools, opt.window) {
		fatal("no tool named %q; configured tools are %s", opt.window, strings.Join(toolNames(tools), ", "))
	}

	s := session.NewSession(name, absoluteDirectory)
	existed, err := s.Exists()
	if err != nil {
		fatal("%v", err)
	}

	current := plan{
		session:   name,
		directory: absoluteDirectory,
		config:    configPath,
		file:      file,
		selected:  window,
		tools:     tools,
		existed:   existed,
		action:    "none",
	}

	if opt.dryRun {
		printPlan(opt, s, current)
		return
	}

	switch {
	case !existed:
		if err := s.Create(tools); err != nil {
			fatal("creating session: %v", err)
		}
		current.action = "created"
	case opt.refresh:
		if err := s.Refresh(tools, opt.window); err != nil {
			fatal("refreshing session: %v", err)
		}
		current.action = "refreshed"
	case opt.sync:
		created, err := s.Sync(tools)
		if err != nil {
			fatal("syncing session: %v", err)
		}
		if len(created) > 0 {
			current.action = "synced"
			if !opt.json {
				fmt.Printf("added %s\n", strings.Join(created, ", "))
			}
		}
	}

	// A command only sees --file at the moment it starts. Anything already
	// running was started without it, and dropping that silently is the worst
	// option: the user asked for a file to reach a tool and would find a session
	// that never saw it.
	if file != "" && existed && current.action != "refreshed" {
		warn("session %s was already running; windows that did not just start have not seen --file (rerun with --refresh to respawn them, or --send to hand it to a running tool)", name)
	}

	if err := s.SelectWindow(window); err != nil {
		warn("could not select window %q: %v", window, err)
	}

	if opt.noAttach {
		if opt.json {
			printPlan(opt, s, current)
		}
		return
	}
	if err := s.AttachOrSwitch(); err != nil {
		fatal("attaching to session: %v", err)
	}
}

func named(tools []config.Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func toolNames(tools []config.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

// actionWord reports what actually happened rather than what was asked for, so
// a caller can tell a no-op apart from a change without diffing state itself.
func actionWord(changed bool, word string) string {
	if changed {
		return word
	}
	return "none"
}

func report(opt options, name, action string) {
	if opt.json {
		emit(jsonAction{Session: name, Action: action})
		return
	}
	if action != "none" {
		fmt.Printf("%s %s\n", action, name)
	}
}

func printPlan(opt options, s *session.Session, p plan) {
	// Which windows are up right now, so --print can mark the ones a --sync
	// would add. Only meaningful once the session is there.
	live := map[string]bool{}
	if p.existed || p.action != "none" {
		if names, err := s.Windows(); err == nil {
			for _, name := range names {
				live[name] = true
			}
		}
	}

	if opt.json {
		windows := make([]jsonWindow, 0, len(p.tools))
		for _, tool := range p.tools {
			windows = append(windows, jsonWindow{Name: tool.Name, Command: tool.Command, Live: live[tool.Name]})
		}
		emit(jsonPlan{
			Session:   p.session,
			Action:    p.action,
			Directory: p.directory,
			Config:    optional(p.config),
			File:      optional(p.file),
			Exists:    p.existed,
			Select:    p.selected,
			Windows:   windows,
		})
		return
	}

	running := ""
	if p.existed {
		running = " (running)"
	}
	fmt.Printf("session   %s%s\n", p.session, running)
	fmt.Printf("directory %s\n", p.directory)
	if p.config == "" {
		fmt.Printf("config    (none — would create %s)\n", config.DefaultPath())
	} else {
		fmt.Printf("config    %s\n", p.config)
	}
	if p.file == "" {
		fmt.Println("file      (none)")
	} else {
		fmt.Printf("file      %s\n", p.file)
	}
	fmt.Printf("select    %s\n", p.selected)

	fmt.Println("windows")
	width := 0
	for _, tool := range p.tools {
		if len(tool.Name) > width {
			width = len(tool.Name)
		}
	}
	for _, tool := range p.tools {
		marker := " "
		if tool.Name == p.selected {
			marker = "*"
		}
		state := ""
		if p.existed && !live[tool.Name] {
			state = "  (missing)"
		}
		fmt.Printf("  %s %-*s  %s%s\n", marker, width, tool.Name, tool.Command, state)
	}
}

// optional renders an empty string as JSON null: "no file" and "a file named
// nothing" are different claims.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "warning: "+strings.TrimSuffix(format, "\n")+"\n", args...)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}
