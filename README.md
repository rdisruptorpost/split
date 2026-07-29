# split

split is a terminal-native workspace for running shells and coding agents side by side. It combines a compact project sidebar, tmux-style panes, real Windows ConPTY terminals, and a persistent background runtime.

Every pane is an ordinary PowerShell terminal: click a pane and type the same command you would use in Windows Terminal. split passively recognizes Codex and Claude descendants for live sidebar status. Trusted SessionStart hooks record exact provider-owned IDs in SQLite. For Codex sessions opened before their first prompt, split also correlates the live Codex PID with Codex's local diagnostic thread record so a resumed picker selection is not lost. split never guesses which conversation to resume and never copies or reimplements provider transcript storage.

## Current foundation

- Bubble Tea v2 interface with a neutral, high-contrast graphite theme;
- sidebar-only project switching with a continuous selected-row highlight, right-click rename/close actions, and a clickable `+ New project` row;
- nested Codex and Claude rows with loading/working spinners, blocked alerts, completion ticks, interrupted turns, idle markers, and exited markers;
- recursive split-tree layouts with movement, closing, and automatic balancing;
- keyboard prefix controls plus a hoverable right-click pane menu;
- one-click mouse focus, left-drag terminal selection with clipboard copy, and per-pane mouse-wheel scrollback;
- real PowerShell processes hosted through Windows ConPTY;
- headless terminal emulation with 5,000 lines of styled history, complete printable-key forwarding, and a native blinking cursor through Charm's `x/vt` package;
- SQLite persistence for project order, names, roots, live pane working directories, focus, complete split geometry, and provider session IDs;
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

The visible split process is now a lightweight client. On first launch it starts a hidden local runtime automatically, then connects over a current-user-only Windows named pipe. Subsequent launches reconnect to that runtime.

Normal quit is detach: press `q` in navigation mode, use `Ctrl+B`, then `q`, or close the terminal window. Your PowerShell, Codex, Claude, and other child processes continue running. To deliberately terminate every pane and stop the runtime, run:

```powershell
.\split.next.exe server stop
```

Stop the runtime before replacing a development executable that Windows reports as in use. Runtime diagnostics are appended as JSON Lines to `%LOCALAPPDATA%\split\runtime.log`. They include executable paths, pane/project IDs, provider session IDs, working directories, and lifecycle results, but never terminal input, terminal output, prompts, or chat contents. The last events can be inspected with:

```powershell
Get-Content "$env:LOCALAPPDATA\split\runtime.log" -Tail 200
```

For an agent that will resume after an explicit server restart, the useful event chain begins with either `hook-wrapper/invoked` plus `session-capture/binding_written`, or the Codex pre-prompt fallback `model/codex_session_correlated`. It then continues through `server/stop_persisted`, `model/pane_restore_loaded`, and `model/agent_resume_scheduled`. A missing event identifies which boundary failed.

On launch, split installs or updates a small SessionStart handler in the Codex and Claude configuration. It is inert outside a split pane, forwards only provider, session ID, and cwd into `state.db`, and always exits successfully so persistence can never block an agent from opening. Codex asks you to trust new or changed user hooks; use `/hooks` inside Codex once to approve the exact-ID handler. Codex 0.145 runs its pending SessionStart handler when the first turn begins rather than when the picker finishes loading. To cover the period before that first prompt, split reads Codex's local `logs_*.sqlite` database in read-only mode and accepts only a fresh UUID attached to the detected Codex PID. The normal trusted hook remains authoritative once it runs. The command below can reinstall the handler explicitly:

```powershell
.\split.next.exe hooks install
```

To remove only the split-owned handlers while preserving every unrelated Codex and Claude setting, run `hooks uninstall`.

## Persistence model

split stores durable workspace metadata in `%LOCALAPPDATA%\split\state.db` using SQLite in WAL mode. It remembers:

- project names, order, roots, and selected project;
- sidebar visibility and focused pane;
- every pane's custom title and latest PowerShell working directory;
- the full split tree and ratios;
- the native session ID and launch directory for an active Codex or Claude process.

PowerShell emits an invisible working-directory signal at every prompt, and the runtime checkpoints changes to SQLite once per second even with no UI client attached. The background runtime owns the live ConPTY handles, terminal emulators, and child processes, so a normal detach/reconnect still returns to the exact live terminal.

An explicit `server stop`, process crash, sign-out, or reboot necessarily ends those OS processes. On the next launch, split creates fresh PowerShell terminals at their saved directories and automatically runs `codex resume <session-id>` or `claude --resume <session-id>` only when an exact provider session ID was captured for that pane. Other terminal programs—and agents whose exact ID was not captured—restart as ordinary PowerShell panes.

`state.db` is the sole durable metadata store. On the first launch of this version, the legacy `%LOCALAPPDATA%\Split` directory is safely renamed to lowercase `%LOCALAPPDATA%\split`; valid records from the old `session-events` JSON spool are imported, ambiguous last-session markers are discarded, and both obsolete JSON directories are removed. `session-hook.ps1` is managed integration code rather than a second data store; `state.db-wal` and `state.db-shm` are normal temporary SQLite companion files.

## Controls

split starts in navigation mode.

| Key | Action |
| --- | --- |
| `h/j/k/l` or arrows | Move between panes |
| `Tab` | Move focus between panes and the project sidebar |
| `Enter` | Enter the focused terminal |
| Mouse click | Focus a terminal and immediately enter input mode |
| `Alt` + right-drag | Resize the nearest available pane dividers, or resize the sidebar when the pointer starts there |
| Left-drag terminal text | Select text in the live screen or scrolled history |
| `Ctrl+C` in a terminal | Copy the active selection; without a selection, send the normal interrupt to PowerShell/Codex/Claude |
| Right-click selected terminal | Copy the selection to the system clipboard |
| `Ctrl+V` / terminal paste | Paste through the ConPTY, including bracketed-paste mode when requested by the child app |
| Mouse wheel | Scroll the terminal under the pointer; mouse-aware full-screen TUIs receive the wheel event |
| Click anywhere in sidebar | Leave terminal-input mode and focus project navigation; `q` now detaches |
| Click an agent row | Select its project and pane while keeping keyboard focus in the sidebar |
| Right-click project | Open project actions for Rename project or Close project |
| Right-click pane without a selection | Open pane actions, including Rename terminal |
| `[` / `]` | Previous or next project |
| `q` or `Ctrl+C` | Detach the UI while in navigation mode |
| `Ctrl+B` | Open the one-shot command prefix |

Mouse-wheel history is independent for every pane. The pane title shows the number of lines above the live bottom, the live cursor is hidden while inspecting history, and typing or pasting snaps back to current output. Selection coordinates follow terminal history rather than screen pixels, so text copied after scrolling is the text that was actually highlighted. A normal click, key, paste, wheel event, resize, or completed copy clears the selection. During `Alt` + right-drag, the pointer's quadrant chooses a corner; an outer edge with no split falls back to that pane's nearest internal divider. Split ratios update live, ConPTY resizing is deferred until release, and both pane ratios and sidebar width persist. The pane right-click menu renames the terminal, creates a PowerShell split to the right or below, creates a new project, moves the focused pane, balances the layout, or closes the pane. A custom terminal name replaces the pane-frame title and its detected Codex or Claude label in the project sidebar while preserving live agent status. The project right-click menu renames a project or closes all of its panes and removes it; Close project is disabled for the final remaining project. Hover selects a row and left-click activates it.

Prefix commands:

| Key | Action |
| --- | --- |
| `v` or `%` | split right with a PowerShell terminal |
| `s` or `"` | split down with a PowerShell terminal |
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

The hidden runtime owns the real `app.Model`, SQLite store, terminal emulators, ConPTY handles, child processes, and agent monitor. The monitor walks each PowerShell descendant tree and combines the recognized process with live bottom-of-screen and OSC-title signals. Users launch programs normally; only recovery after a full runtime loss invokes a captured provider session through its documented native resume command. The visible Bubble Tea client forwards resize, key, paste, and mouse messages, renders versioned frames received over the local named pipe, and applies one-shot clipboard requests in the user-facing terminal. Disconnecting a client resets transient menus and focus state but does not close a terminal. An explicit stop checkpoints cwd/layout state, closes every ConPTY, and exits the runtime before acknowledging the command.

This terminal-first boundary keeps split provider-agnostic while leaving room for future structured integrations, such as a native Codex app-server pane, without making ordinary terminal workflows depend on them.
