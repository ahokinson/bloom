# bloom

Given a project directory, bloom creates a tmux session with the tool windows defined in config. If a session already exists for that directory, it attaches to it.

## Philosophy

Tmux panes split your attention. Windows give you the same multiplexing without the visual noise.

## Build

```
task build
```

That leaves a binary at `bin/bloom`; putting it on your `PATH` is up to whatever manages the rest of your machine.

Requires [Go](https://go.dev), [Task](https://taskfile.dev) and [tmux](https://github.com/tmux/tmux).

## Usage

```
bloom [flags] [directory]
```

With no arguments, bloom uses the current directory. The session name is derived from the directory basename unless `--session` overrides it.

| Flag | |
|---|---|
| `--session NAME` | Session name. Needed wherever the directory basename is not unique. |
| `--file PATH` | A file to expose to tool commands as `{{.File}}`. |
| `--window NAME` | Window to focus, and the one `--refresh` and `--send` target. Defaults to `select`. |
| `--refresh` | Respawn windows in a session that already exists. |
| `--sync` | Add windows the session is missing; leave running ones alone. |
| `--send TEXT` | Paste text into a window and submit it. `-` reads stdin. |
| `--kill` | Tear the session down. |
| `--resolve` | Print the resolved session name and exit; touches nothing. |
| `--no-attach` | Set the session up but stay where you are. |
| `--print` | Show the resolved session, windows and commands; change nothing. |
| `--json` | Emit the result as JSON. Implies `--no-attach`. |
| `--version` | Print the version. |

Flags come before the directory. Go's flag parsing stops at the first positional argument, so a flag written after it would never be applied — bloom rejects that rather than dropping it in silence.

Running bloom from inside tmux switches the current client to the session rather than trying to nest an attach.

`--kill`, `--send` and `--resolve` each answer and exit before a session is built, so they reject the flags that only make sense while building one. `bloom --resolve --refresh .` is an error, not a `--resolve` with a `--refresh` quietly discarded. `--print` likewise rejects `--refresh` and `--sync`, since the plan it shows is the same either way.

## Templating

A tool command may interpolate `{{.Dir}}`, `{{.Name}}` and `{{.File}}`:

```yaml
  - name: assistant
    command: some-tool{{if .File}} --context {{.File}}{{end}}
```

`File` is empty when `--file` was not given, so that line is plain `some-tool` on an ordinary run — there is no second configuration entry for the case without one. Commands containing no `{{` skip the template engine entirely and are passed through byte-identical: a bare `nvim` or a shell one-liner full of `$HOME` and pipes never asked to be templated, and nothing in it can fail to parse.

Only the *path* is ever interpolated, never the file's contents. bloom does not read the file; splicing arbitrary text into a command is how you get a window that fails to start, or one that runs whatever the file happened to contain.

Because a session's windows are only built once, a second `bloom` against a running session will not deliver a new `--file` — it warns instead. `--refresh` respawns with the new value, killing whatever was in the window. With `--window` it respawns just that one; without, every window.

## Automating

`--json` reports what happened rather than making you diff tmux to find out. `action` is one of `none`, `created`, `refreshed`, `synced`, `sent` or `killed` — and it says what *happened*, not what was asked for, so killing a session that was not running reports `none`.

Every JSON form carries `session` and `action`, so a consumer can read the outcome without knowing which flag produced it. `exists` means *the session was already running before bloom touched it* — which is why creating one reports `false`:

```
$ bloom --json .
{
  "session": "my_project",
  "action": "created",
  "directory": "/Users/me/src/my-project",
  "config": "/Users/me/.config/bloom/config.yml",
  "file": null,
  "exists": false,
  "select": "nvim",
  "windows": [ { "name": "nvim", "command": "nvim", "live": true } ]
}
```

`config` reports which file the layout came from, since a project `.bloom.yml` silently winning over the global one is otherwise invisible.

`--resolve` answers "what would you call this?" without reading config, creating a default config, requiring the directory to exist, or even needing tmux installed:

```
$ bloom --resolve --session 'TICKET-1/my-repo'
TICKET_1_my_repo
```

That last part matters for anything that computes a name and hands it over. Don't reimplement the substitution — ask, or the copy drifts and you get two sessions where you meant one.

`--kill` is likewise lexical, so a session outlives its checkout only as long as you want it to:

```
$ rm -rf ~/src/gone && bloom --kill ~/src/gone
```

**`--sync` vs `--refresh`.** Adding a tool to your config never reaches a running session on its own. `--sync` creates the windows it is missing and leaves everything else running; `--refresh` kills and respawns. Reach for `--sync` unless you specifically want the restart.

**`--send`** delivers text to a live window without restarting anything, which is the difference between giving a running tool new context and starting it over. It goes through a paste buffer with bracketed paste, not `send-keys`, because multi-line text sent as keystrokes arrives as one Enter per line — the first line submitted and the rest typed into whatever that produced.

```
$ bloom --send - --window assistant --session my_project < CONTEXT.md
```

Prefer `-` and stdin for anything long: it never goes near a shell, so quoting cannot mangle it and a `$(...)` in the text stays text. Note the tradeoff this makes against `--file`, which only ever passes a *path*: `--send` does put text in front of a running program, and that program decides what to do with it.

Trailing newlines are trimmed before the paste, because a file redirected in ends with one and pasting it before the submitting Enter would submit twice. `--send ""` is still a real instruction — a bare Enter, submitting whatever the window is already holding. `--send -` with nothing redirected is an error rather than a wait that never ends.

### Names are matched exactly

tmux resolves a target as an exact name, then as the *start* of a name, then as a glob. bloom asks for exact matches everywhere, which is the difference between working and occasionally catastrophic: with a session named `api_v2` running and none named `api`, a prefix match means `bloom ~/src/api` attaches to the wrong project's session and `bloom --kill ~/src/api` tears `api_v2` down with everything running in it. The same applies to windows, where `--send --window web` would otherwise paste into `webpack`.

## Configuration

bloom looks for config in this order:

1. `<project>/.bloom.yml` (project-specific)
2. `$XDG_CONFIG_HOME/bloom/config.yml` (global, defaults to `~/.config/bloom/config.yml`)

If no config is found, bloom creates a default one with a single window running your shell — except under `--print`, which promises to change nothing and so uses that default in memory and tells you where it *would* have written it.

### Example `.bloom.yml`

```yaml
select: editor
tools:
  - name: editor
    command: nvim
  - name: server
    command: task dev
```

`select` sets which window is focused on attach, and must name one of the tools. Left out, it falls back to the first tool. Each tool gets its own tmux window running the specified command.

### Tool names

A tool's name becomes its window name, and bloom addresses windows by name — `--send`, `--refresh` and `--sync` all target that way. tmux resolves a window target as an index, then an exact name, then a *name prefix*, then a glob, and it splits `session:window.pane` on the punctuation before any of that. A name that collides with the syntax is not reliably addressable, so it is rejected when the config is read rather than misbehaving later in a way that looks like tmux being haunted:

| Not allowed | Because tmux reads it as |
|---|---|
| `web.1` | window `web`, pane `1` |
| `a:b` | the `session:window` separator |
| `2` | window *index* 2, not a name |
| `log*`, `log[1]` | a glob pattern |
| `my tool` | two arguments |
| `=web`, `@1`, `{start}`, `^`, `!` | a target token, not a name |

Every problem in a config is reported at once, so one run is enough to fix it. Duplicate names are rejected for the same reason: two windows sharing a name leave `--send` with no way to say which one you meant.
