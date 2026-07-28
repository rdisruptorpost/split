package main

import (
	"errors"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"split/internal/hooks"
	"split/internal/sessionserver"
	"split/internal/state"
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
			// Split no longer uses provider hooks. Keep old installed hooks quiet
			// while users migrate to terminal-owned live process persistence.
			return nil
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
	if len(arguments) != 1 {
		return errors.New("usage: split hooks uninstall")
	}
	if arguments[0] == "install" {
		fmt.Fprintln(os.Stdout, "Split now persists live terminal processes; provider hooks are no longer required.")
		return nil
	}
	if arguments[0] != "uninstall" {
		return errors.New("usage: split hooks uninstall")
	}
	paths, err := hooks.DefaultPaths()
	if err != nil {
		return err
	}
	results, err := hooks.UninstallAll(paths)
	if err != nil {
		return err
	}
	for _, result := range results {
		status := "not installed"
		if result.Changed {
			status = "removed"
		}
		fmt.Fprintf(os.Stdout, "%s: %s (%s)\n", result.Provider, status, result.Path)
		if result.BackupPath != "" {
			fmt.Fprintf(os.Stdout, "  backup: %s\n", result.BackupPath)
		}
	}
	return nil
}

func runServerCommand(arguments []string) error {
	if len(arguments) == 3 && arguments[0] == "run" {
		return sessionserver.Run(arguments[1], arguments[2])
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
		if err := sessionserver.Stop(statePath); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "Split runtime stopped; terminal processes were closed.")
		return nil
	}
	return errors.New("usage: split server stop [state-path]")
}
