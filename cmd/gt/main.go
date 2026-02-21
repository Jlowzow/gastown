// gt is the Gas Town CLI for managing multi-agent workspaces.
package main

import (
	"os"

	"github.com/steveyegge/gastown/internal/amux"
	"github.com/steveyegge/gastown/internal/cmd"
	"github.com/steveyegge/gastown/internal/session"
)

func init() {
	// Register amux backend so session.NewBackend() can return it
	// when GT_SESSION_BACKEND=amux, avoiding import cycles.
	session.RegisterAmuxFactory(func() session.SessionBackend {
		return amux.NewAmux()
	})
}

func main() {
	os.Exit(cmd.Execute())
}
