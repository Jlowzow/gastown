package session

import (
	"github.com/steveyegge/gastown/internal/tmux"
)

// SessionBackend abstracts session management operations, enabling different
// backend implementations (tmux, amux) to be swapped via configuration
// without modifying call sites.
//
// Phase 1: interface definition only. Call sites continue using *tmux.Tmux
// directly. Phase 2 (gt-8g9) migrates call sites to use this interface.
type SessionBackend interface {
	// Session lifecycle
	NewSession(name, workDir string) error
	NewSessionWithCommand(name, workDir, command string) error
	NewSessionWithCommandAndEnv(name, workDir, command string, env map[string]string) error
	KillSession(name string) error
	KillSessionWithProcesses(name string) error
	HasSession(name string) (bool, error)
	ListSessions() ([]string, error)
	GetSessionSet() (*tmux.SessionSet, error)

	// Input
	SendKeys(session, keys string) error
	NudgeSession(session, message string) error

	// Output capture
	CapturePane(session string, lines int) (string, error)

	// Environment
	SetEnvironment(session, key, value string) error
	GetEnvironment(session, key string) (string, error)

	// Health
	IsAgentRunning(session string, expectedPaneCommands ...string) bool
	IsAgentAlive(session string) bool
	IsAvailable() bool
}
