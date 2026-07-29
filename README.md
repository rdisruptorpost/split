# Split

Split is a terminal-native workspace for running shells and coding agents side by side. It combines a compact project sidebar, tmux-style panes, real Windows ConPTY terminals, and a persistent background runtime.

Every pane is an ordinary PowerShell terminal. Split does not special-case launching or resuming Codex, Claude Code, or another terminal application: click a pane and type the same command you would use in Windows Terminal. The runtime passively recognizes Codex and Claude descendants so the sidebar can report their live state without taking ownership of their commands, hooks, or transcript metadata.

## Current foundation

- Bubble Tea v2 interface with a neutral, high-contrast graphite theme;
- sidebar-only project switching with a continuous selected-row highlight, right-click rename/close actions, and a clickable `+ New project` row;
- nested Codex and Claude rows with loading/working spinners, blocked alerts, completion ticks, interrupted turns, idle markers, and exited markers;
- recursive split-tree layouts with movement, closing, and automatic balancing;
- keyboard prefix controls plus a hoverable right-click pane menu;
- one-click mouse focus plus per-pane mouse-wheel scrollback;
- real PowerShell processes hosted through Windows ConPTY;
- headless terminal emulation with 5,000 lines of styled history, complete printable-key forwarding, and a native blinking cursor through Charm's `x/vt` package;
- SQLite persistence for project order, names, roots, pane working directories, focus, and complete split geometry;
- a per-user background runtime that keeps live terminal processes and their in-memory terminal buffers alive while the UI is detached.

## Run

```powershell
go run .
```

Or build a review binary:

```powershell
go build -o split.next.exe .
.\split.next.exe
```

The visible Split process is now a lightweight client. On first launch it starts a hidden local runtime automatically, then connects over a current-user-only Windows named pipe. Subsequent launches reconnect to that runtime.

Normal quit is detach: press `q` in navigation mode, use `Ctrl+B`, then `q`, or close the terminal window. Your PowerShell, Codex, Claude, and other child processes continue running. To deliberately terminate every pane and stop the runtime, run:

```powershell
.\split.next.exe server stop
```

Stop the runtime before replacing a development executable that Windows reports as in use. Runtime diagnostics are written to `%LOCALAPPDATA%\Split\runtime.log`.

Legacy `split hook ...` invocations exit successfully as harmless no-ops, so an old provider configuration will not break startup. Provider hooks are no longer needed. To remove only the Split-owned handlers while preserving every unrelated Codex and Claude setting, run:

```powershell
.\split.next.exe hooks uninstall
```

## Persistence model

Split stores durable workspace metadata in `%LOCALAPPDATA%\Split\state.db` using SQLite in WAL mode. It remembers:

- project names, order, roots, and selected project;
- sidebar visibility and focused pane;
- every pane's custom title and working directory;
- the full split tree and ratios.

The background runtime owns the live ConPTY handles, terminal emulators, and child processes. Detaching and reconnecting therefore returns to the exact live terminal, including a Codex or Claude session launched by typing its command in PowerShell.

An explicit `server stop`, process crash, sign-out, or reboot ends those live processes. On the next launch, Split restores the durable layout as fresh PowerShell terminals in their saved working directories. Provider transcripts remain provider-owned; use the CLI's normal resume workflow inside the appropriate pane when recovering after a full runtime stop.

## Controls

Split starts in navigation mode.

| Key | Action |
| --- | --- |
| `h/j/k/l` or arrows | Move between panes |
| `Tab` | Move focus between panes and the project sidebar |
| `Enter` | Enter the focused terminal |
| Mouse click | Focus a terminal and immediately enter input mode |
| Mouse wheel | Scroll the terminal under the pointer; mouse-aware full-screen TUIs receive the wheel event |
| Click anywhere in sidebar | Leave terminal-input mode and focus project navigation; `q` now detaches |
| Click an agent row | Select its project and pane while keeping keyboard focus in the sidebar |
| Right-click project | Open project actions for Rename project or Close project |
| Right-click pane | Open pane actions, including Rename terminal |
| `[` / `]` | Previous or next project |
| `q` or `Ctrl+C` | Detach the UI while in navigation mode |
| `Ctrl+B` | Open the one-shot command prefix |

Mouse-wheel history is independent for every pane. The pane title shows the number of lines above the live bottom, the live cursor is hidden while inspecting history, and typing or pasting snaps back to current output. The pane right-click menu renames the terminal, creates a PowerShell split to the right or below, creates a new project, moves the focused pane, balances the layout, or closes the pane. A custom terminal name replaces the pane-frame title and its detected Codex or Claude label in the project sidebar while preserving live agent status. The project right-click menu renames a project or closes all of its panes and removes it; Close project is disabled for the final remaining project. Hover selects a row and left-click activates it.

Prefix commands:

| Key | Action |
| --- | --- |
| `v` or `%` | Split right with a PowerShell terminal |
| `s` or `"` | Split down with a PowerShell terminal |
| `c` | Create a new single-terminal project |
| `x` | Close the active pane or project |
| `h/j/k/l` or arrows | Move the active pane in that direction |
| `=` or `e` | Balance all panes in the active project |
| `[` / `]` | Previous or next project |
| `n` | Return to navigation mode |
| `w` | Toggle the sidebar |
| `b` | Send a literal `Ctrl+B` to the terminal |
| `q` | Detach the UI |

New projects and splits always start as PowerShell terminals at the current project root. Once focused, they accept arbitrary terminal programs exactly as Windows Terminal does.

## Architecture

The hidden runtime owns the real `app.Model`, SQLite store, terminal emulators, ConPTY handles, child processes, and agent monitor. The monitor walks each PowerShell descendant tree and combines the recognized process with live bottom-of-screen and OSC-title signals; it never launches or resumes the provider itself. The visible Bubble Tea client only forwards resize, key, paste, and mouse messages and renders versioned frames received over the local named pipe. Disconnecting a client resets transient menus and focus state but does not close a terminal. An explicit stop persists the model, closes every ConPTY, and exits the runtime before acknowledging the command.

This terminal-first boundary keeps Split provider-agnostic while leaving room for future structured integrations, such as a native Codex app-server pane, without making ordinary terminal workflows depend on them.
