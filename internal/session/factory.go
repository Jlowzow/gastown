package session

import (
	"github.com/steveyegge/gastown/internal/tmux"
)

// NewBackend returns the configured session backend.
// Checks GT_SESSION_BACKEND env var: "amux" uses the registered amux factory,
// anything else returns tmux (default).
//
// To use amux, call RegisterAmuxFactory in package init or main:
//
//	session.RegisterAmuxFactory(func() session.SessionBackend { return amux.NewAmux() })
func NewBackend() SessionBackend {
	if BackendName() == "amux" && amuxFactory != nil {
		return amuxFactory()
	}
	return tmux.NewTmux()
}

// amuxFactory is set by RegisterAmuxFactory to avoid import cycles.
var amuxFactory func() SessionBackend

// RegisterAmuxFactory registers the amux backend constructor.
// This is called from a higher-level package to avoid import cycles
// between session and amux packages.
func RegisterAmuxFactory(factory func() SessionBackend) {
	amuxFactory = factory
}
