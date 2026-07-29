package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"split/internal/diagnostics"
	"split/internal/hooks"
	"split/internal/sessionserver"
	"split/internal/state"
	"split/internal/usage"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "split:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "server":
			return runServerCommand(os.Args[2:])
		case "hook":
			return runHookCommand(os.Args[2:])
		case "hooks":
			return runHooksCommand(os.Args[2:])
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine working directory: %w", err)
	}
	statePath, err := state.DefaultPath()
	if err != nil {
		return err
	}
	ensureSessionHooks(statePath)
	client, err := sessionserver.ConnectOrStart(cwd, statePath)
	if err != nil {
		return err
	}
	defer client.Close()

	program := tea.NewProgram(client)
	if _, err := program.Run(); err != nil {
		return err
	}
	return client.Err()
}

func runHooksCommand(arguments []string) error {
	if len(arguments) != 1 || (arguments[0] != "install" && arguments[0] != "uninstall") {
		return errors.New("usage: split hooks install|uninstall")
	}
	paths, err := hooks.DefaultPaths()
	if err != nil {
		return err
	}

	var results []hooks.Result
	if arguments[0] == "install" {
		statePath, err := state.DefaultPath()
		if err != nil {
			return err
		}
		executable, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locate split executable: %w", err)
		}
		results, err = hooks.InstallAll(paths, executable, statePath)
	} else {
		results, err = hooks.UninstallAll(paths)
	}
	if err != nil {
		return err
	}
	for _, result := range results {
		status := "already installed"
		if arguments[0] == "uninstall" {
			status = "not installed"
		}
		if result.Changed {
			if arguments[0] == "install" {
				status = "installed"
			} else {
				status = "removed"
			}
		}
		fmt.Fprintf(os.Stdout, "%s: %s (%s)\n", result.Provider, status, result.Path)
		if result.BackupPath != "" {
			fmt.Fprintf(os.Stdout, "  backup: %s\n", result.BackupPath)
		}
	}
	return nil
}

func ensureSessionHooks(statePath string) {
	paths, err := hooks.DefaultPaths()
	if err != nil {
		_ = diagnostics.Append(statePath, "hook-install", "paths_failed", nil, err)
		fmt.Fprintln(os.Stderr, "split: provider session persistence unavailable:", err)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		_ = diagnostics.Append(statePath, "hook-install", "executable_failed", nil, err)
		fmt.Fprintln(os.Stderr, "split: provider session persistence unavailable:", err)
		return
	}
	results, err := hooks.InstallAll(paths, executable, statePath)
	if err != nil {
		_ = diagnostics.Append(
			statePath,
			"hook-install",
			"install_failed",
			diagnostics.Fields{"executable": executable},
			err,
		)
		// A malformed third-party config must not prevent split itself from
		// starting. The explicit hooks install command still reports the error.
		fmt.Fprintln(os.Stderr, "split: provider session persistence unavailable:", err)
		return
	}
	fields := diagnostics.Fields{"executable": executable}
	for _, result := range results {
		status := "unchanged"
		if result.Changed {
			status = "updated"
		}
		fields[result.Provider] = status
	}
	_ = diagnostics.Append(statePath, "hook-install", "ready", fields, nil)
}

func runHookCommand(arguments []string) error {
	if len(arguments) == 3 && arguments[0] == "provider-usage" {
		return runProviderUsageHook(arguments[1], arguments[2])
	}
	if len(arguments) != 3 || arguments[0] != "session-start" {
		return nil
	}
	provider := arguments[1]
	statePath := arguments[2]
	paneID := os.Getenv("SPLIT_PANE_ID")
	fields := diagnostics.Fields{
		"provider":        provider,
		"pane_id":         paneID,
		"split_env":       os.Getenv("SPLIT_ENV"),
		"term_program":    os.Getenv("TERM_PROGRAM"),
		"hook_executable": os.Getenv("SPLIT_HOOK_EXE"),
	}
	if cwd, err := os.Getwd(); err == nil {
		fields["process_cwd"] = cwd
	}
	_ = diagnostics.Append(statePath, "hook-helper", "received", fields, nil)
	if os.Getenv("SPLIT_ENV") != "1" || paneID == "" {
		_ = diagnostics.Append(
			statePath,
			"hook-helper",
			"skipped",
			fields,
			errors.New("split pane environment is incomplete"),
		)
		return nil
	}
	// The managed wrapper always exits successfully so persistence can never
	// prevent Codex or Claude from opening. Returning the recorder error here
	// keeps direct invocation observable for diagnostics and tests.
	err := hooks.RecordSessionStart(provider, paneID, statePath, os.Stdin)
	if err != nil {
		_ = diagnostics.Append(statePath, "hook-helper", "failed", fields, err)
		return err
	}
	_ = diagnostics.Append(statePath, "hook-helper", "completed", fields, nil)
	return nil
}

func runProviderUsageHook(provider, statePath string) error {
	if provider != "claude" || os.Getenv("SPLIT_ENV") != "1" ||
		os.Getenv("SPLIT_PANE_ID") == "" || statePath == "" {
		return nil
	}
	value, available, err := usage.ParseClaudeStatusLine(os.Stdin, time.Now())
	if err != nil {
		_ = diagnostics.Append(
			statePath,
			"usage",
			"claude_status_line_failed",
			diagnostics.Fields{"provider": provider},
			err,
		)
		return err
	}
	if !available {
		return nil
	}
	store, err := state.Open(statePath)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.UpsertProviderUsage(value); err != nil {
		_ = diagnostics.Append(
			statePath,
			"usage",
			"claude_cache_write_failed",
			diagnostics.Fields{"provider": provider},
			err,
		)
		return err
	}
	return nil
}

func runServerCommand(arguments []string) error {
	if len(arguments) == 3 && arguments[0] == "run" {
		return sessionserver.RunWithUsage(arguments[1], arguments[2])
	}
	if len(arguments) >= 1 && len(arguments) <= 2 && arguments[0] == "stop" {
		statePath := ""
		if len(arguments) == 2 {
			statePath = arguments[1]
		} else {
			var err error
			statePath, err = state.DefaultPath()
			if err != nil {
				return err
			}
		}
		_ = diagnostics.Append(statePath, "client", "server_stop_requested", nil, nil)
		if err := sessionserver.Stop(statePath); err != nil {
			_ = diagnostics.Append(statePath, "client", "server_stop_failed", nil, err)
			return err
		}
		_ = diagnostics.Append(statePath, "client", "server_stop_acknowledged", nil, nil)
		fmt.Fprintln(os.Stdout, "split runtime stopped; terminal processes were closed.")
		return nil
	}
	return errors.New("usage: split server stop [state-path]")
}
