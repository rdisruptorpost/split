# Split

Split is a terminal-native workspace for running coding agents side by side.
The current checkpoint is a Bubble Tea v2 workspace foundation with:

- a compact, sidebar-only project switcher;
- border-embedded pane titles that leave more room for terminal content;
- independent project workspaces with recursive tmux-style split layouts and automatic balancing;
- terminal, navigation, and one-shot prefix modes;
- real processes hosted through Windows ConPTY;
- terminal emulation and a native blinking cursor through Charm's headless `x/vt` package;
- a neutral graphite theme with restrained warm-gray and state-color accents;
- a launcher for PowerShell, Codex CLI, and Claude Code;
- per-pane profile badges and live, exited, or failed process state;
- clickable sidebar project rows, a `+ New project` control, and one-click terminal input focus;
- SQLite-backed project, pane-layout, and agent-session restoration.

## Run

```powershell
go run .
```

The first build may download the Go 1.25 toolchain and module dependencies.

For exact Codex and Claude session restoration, build Split and install its
session-start hooks:

```powershell
go build -o split.exe .
.\split.exe hooks install
```

The installer merges Split's hooks with existing provider configuration and
creates a one-time `.split-backup` beside each changed file. Re-run it if the
Split executable moves to a different path.

## Persistence

Split stores its state in `%LOCALAPPDATA%\Split\state.db`. It remembers project
names and order, roots, the selected project and pane, sidebar visibility, pane
profiles and working directories, and each project's complete split tree and
ratios.

PowerShell panes restart as fresh shells. Codex and Claude panes resume their
provider-owned conversations by session ID, so Split does not copy or duplicate
agent transcripts. Restored background projects are started lazily when first
selected.

## Controls

Split starts in navigation mode.

| Key | Action |
| --- | --- |
| `h/j/k/l` or arrows | Move between panes |
| `Tab` | Move focus between panes and sidebar |
| `Enter` | Enter the focused terminal |
| Mouse click | Focus a terminal and immediately enter input mode |
| Right-click pane | Open the pane action menu |
| `q` | Quit while in navigation mode |
| `Ctrl+B` | Open the one-shot command prefix |

The right-click pane menu provides mouse access to split-right, split-below,
and new-project launchers for PowerShell, Codex, and Claude Code. It also supports
directional pane movement, balancing, and closing. Hover selects a row; click
a row to activate it, or press `Esc` to close the menu.

Prefix commands:

| Key | Action |
| --- | --- |
| `v` or `%` | Split right with PowerShell |
| `s` or `"` | Split down with PowerShell |
| `a` | Open the process launcher |
| `c` | Create a PowerShell project workspace |
| `x` | Close the active pane or project |
| `h/j/k/l` or arrows | Move the active pane in that direction |
| `=` or `e` | Balance all panes in the active project |
| `[` / `]` | Previous or next project |
| `n` | Enter navigation mode |
| `w` | Toggle the sidebar |
| `b` | Send a literal `Ctrl+B` to the terminal |
| `q` | Quit Split |

Launcher controls:

| Key | Action |
| --- | --- |
| `j/k` or arrows | Select PowerShell, Codex, or Claude Code |
| `Enter` or `v` | Open the selection in a right-hand split |
| `s` | Open the selection in a lower split |
| `t` | Open the selection in a new project |
| `Esc` or `q` | Close the launcher |

Launch profiles are discovered from `PATH` at startup and inherit the project
root as their working directory. Unavailable tools remain visible and are marked
`not found` rather than disappearing from the interface. The sidebar keeps this
variable launcher list out of the permanent layout; use `Ctrl+B`, then `a`, or
the right-click pane menu when you want to launch a process. New projects created
from the sidebar start as independent, single-pane PowerShell workspaces in the current root.

New splits and layouts left after closing a pane are balanced automatically. If
an agent pane becomes too narrow or short for its full-screen TUI, Split shows a
clean recovery message instead of partially rendered terminal fragments; resize,
balance, close a pane, or reopen the agent in a new project to restore its view.

## Architecture

The Bubble Tea model owns project workspaces, focus, keyboard modes, and binary
split trees. SQLite state and provider-session bindings are isolated behind
`internal/state`; PTY sessions remain behind `internal/terminal`. Pane rendering
does not depend on either persistence or process-management details. This leaves
room for future native pane types such as a structured Codex app-server client.

