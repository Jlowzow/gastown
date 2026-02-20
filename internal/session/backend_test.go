package session_test

import (
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/tmux"
)

// Compile-time check: *tmux.Tmux must satisfy SessionBackend.
var _ session.SessionBackend = (*tmux.Tmux)(nil)
