// Command codex-agents is the cockpit's entry point for slice #2: a
// read-only terminal list of every codex thread, sourced from codexstate
// (thread data) and tmuxstatus (open/closed liveness), rendered by the ui
// package.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dzungtr/codex-agents/internal/codexstate"
	"github.com/dzungtr/codex-agents/internal/tmuxstatus"
	"github.com/dzungtr/codex-agents/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "codex-agents:", err)
		os.Exit(1)
	}
}

func run() error {
	codexHome, err := resolveCodexHome()
	if err != nil {
		return err
	}

	result, err := codexstate.LoadThreads(codexHome)
	if err != nil {
		return err
	}

	live, err := tmuxstatus.ListLiveSessions()
	if err != nil {
		return err
	}
	liveSet := tmuxstatus.NewLiveSet(live)

	rows := make([]ui.Row, 0, len(result.Threads))
	for _, t := range result.Threads {
		rows = append(rows, ui.Row{
			Thread: t,
			Status: tmuxstatus.StatusFor(t.ID, liveSet),
		})
	}

	_, err = tea.NewProgram(ui.New(rows), tea.WithAltScreen()).Run()
	return err
}

// resolveCodexHome honors $CODEX_HOME (as codex's own CLI does) before
// falling back to the default ~/.codex.
func resolveCodexHome() (string, error) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return home, nil
	}
	return codexstate.DefaultCodexHome()
}
