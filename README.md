# bloom

Given a project directory, bloom creates a tmux session with the tool windows defined in config. If a session already exists for that directory, it attaches to it.

## Philosophy

Tmux panes split your attention. Windows give you the same multiplexing without the visual noise.

## Install

```
task install
```

Requires [Go](https://go.dev), [Task](https://taskfile.dev) and [tmux](https://github.com/tmux/tmux).

## Usage

```
bloom [directory]
```

With no arguments, bloom uses the current directory. The session name is derived from the directory basename.

## Configuration

bloom looks for config in this order:

1. `<project>/.bloom.yml` (project-specific)
2. `$XDG_CONFIG_HOME/bloom/config.yml` (global, defaults to `~/.config/bloom/config.yml`)

If no config is found, bloom creates a default config with a shell window.

### Example `.bloom.yml`

```yaml
select: editor
tools:
  - name: editor
    command: nvim
  - name: server
    command: task dev
```

`select` sets which window is focused on attach (defaults to your shell). Each tool gets its own tmux window running the specified command.
