package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"split/internal/app"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "split: determine working directory:", err)
		os.Exit(1)
	}

	model := app.New(cwd)
	defer model.Close()

	program := tea.NewProgram(model)
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "split:", err)
		os.Exit(1)
	}
}
