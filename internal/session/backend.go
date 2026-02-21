package session

import (
	"os"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/tmux"
)

// SessionBackend abstracts session management operations, enabling different
// backend implementations (tmux, amux) to be swapped via configuration
// without modifying call sites.
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

// TmuxExtras is an optional interface for tmux-specific features that
// don't apply to all backends. Use type assertions to check availability.
type TmuxExtras interface {
	SetRemainOnExit(session string, on bool) error
	ConfigureGasTownSession(session string, theme tmux.Theme, rigName, agentName, role string) error
	WaitForCommand(session string, cmds []string, timeout time.Duration) error
	SetAutoRespawnHook(session string) error
	AcceptBypassPermissionsWarning(session string) error
}

// BackendName returns "amux" or "tmux" based on the GT_SESSION_BACKEND env var.
func BackendName() string {
	backend := strings.ToLower(os.Getenv("GT_SESSION_BACKEND"))
	if backend == "amux" {
		return "amux"
	}
	return "tmux"
}
