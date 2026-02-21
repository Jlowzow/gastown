package witness

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/workspace"
)

// Common errors
var (
	ErrNotRunning     = errors.New("witness not running")
	ErrAlreadyRunning = errors.New("witness already running")
)

// Manager handles witness lifecycle and monitoring operations.
// ZFC-compliant: tmux session is the source of truth for running state.
type Manager struct {
	rig *rig.Rig
}

// NewManager creates a new witness manager for a rig.
func NewManager(r *rig.Rig) *Manager {
	return &Manager{
		rig: r,
	}
}

// backend returns the configured session backend (tmux or amux).
func (m *Manager) backend() session.SessionBackend {
	return session.NewBackend()
}

// IsRunning checks if the witness session is active and healthy.
// Checks both session existence AND agent process liveness to avoid
// reporting zombie sessions (session alive but Claude dead) as "running".
// ZFC: session existence is the source of truth for session state,
// but agent liveness determines if the session is actually functional.
func (m *Manager) IsRunning() (bool, error) {
	t := m.backend()
	status := t.CheckSessionHealth(m.SessionName(), 0)
	return status == tmux.SessionHealthy, nil
}

// IsHealthy checks if the witness is running and has been active recently.
// Unlike IsRunning which only checks process liveness, this also detects hung
// sessions where Claude is alive but hasn't produced output in maxInactivity.
// Returns the detailed ZombieStatus for callers that need to distinguish
// between different failure modes.
func (m *Manager) IsHealthy(maxInactivity time.Duration) tmux.ZombieStatus {
	t := m.backend()
	return t.CheckSessionHealth(m.SessionName(), maxInactivity)
}

// SessionName returns the tmux session name for this witness.
func (m *Manager) SessionName() string {
	return session.WitnessSessionName(session.PrefixFor(m.rig.Name))
}

// Status returns information about the witness session.
// ZFC-compliant: session existence is the source of truth.
func (m *Manager) Status() (*tmux.SessionInfo, error) {
	t := m.backend()
	sessionID := m.SessionName()

	running, err := t.HasSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("checking session: %w", err)
	}
	if !running {
		return nil, ErrNotRunning
	}

	// GetSessionInfo is tmux-specific; for other backends, return basic info.
	if tmuxT, ok := t.(*tmux.Tmux); ok {
		return tmuxT.GetSessionInfo(sessionID)
	}
	return &tmux.SessionInfo{Name: sessionID}, nil
}

// witnessDir returns the working directory for the witness.
// Prefers witness/rig/, falls back to witness/, then rig root.
func (m *Manager) witnessDir() string {
	witnessRigDir := filepath.Join(m.rig.Path, "witness", "rig")
	if _, err := os.Stat(witnessRigDir); err == nil {
		return witnessRigDir
	}

	witnessDir := filepath.Join(m.rig.Path, "witness")
	if _, err := os.Stat(witnessDir); err == nil {
		return witnessDir
	}

	return m.rig.Path
}

// Start starts the witness.
// If foreground is true, returns an error (foreground mode deprecated).
// Otherwise, spawns a Claude agent in a tmux session.
// agentOverride optionally specifies a different agent alias to use.
// envOverrides are KEY=VALUE pairs that override all other env var sources.
// ZFC-compliant: no state file, tmux session is source of truth.
func (m *Manager) Start(foreground bool, agentOverride string, envOverrides []string) error {
	t := m.backend()
	sessionID := m.SessionName()

	if foreground {
		// Foreground mode is deprecated - patrol logic moved to mol-witness-patrol
		return fmt.Errorf("foreground mode is deprecated; use background mode (remove --foreground flag)")
	}

	// Check if session already exists
	running, _ := t.HasSession(sessionID)
	if running {
		// Session exists - check if Claude is actually running (healthy vs zombie)
		if t.IsAgentAlive(sessionID) {
			// Healthy - Claude is running
			return ErrAlreadyRunning
		}
		// Zombie - session alive but Claude dead. Kill and recreate.
		if err := t.KillSession(sessionID); err != nil {
			return fmt.Errorf("killing zombie session: %w", err)
		}
	}

	// Note: No PID check per ZFC - session existence is the source of truth

	// Working directory
	witnessDir := m.witnessDir()

	// Ensure runtime settings exist in the shared witness parent directory.
	// Settings are passed to Claude Code via --settings flag.
	// ResolveRoleAgentConfig is internally serialized (resolveConfigMu in
	// package config) to prevent concurrent rig starts from corrupting the
	// global agent registry.
	townRoot := m.townRoot()
	runtimeConfig := config.ResolveRoleAgentConfig("witness", townRoot, m.rig.Path)
	witnessSettingsDir := config.RoleSettingsDir("witness", m.rig.Path)
	if err := runtime.EnsureSettingsForRole(witnessSettingsDir, witnessDir, "witness", runtimeConfig); err != nil {
		return fmt.Errorf("ensuring runtime settings: %w", err)
	}

	// Ensure .gitignore has required Gas Town patterns
	if err := rig.EnsureGitignorePatterns(witnessDir); err != nil {
		style.PrintWarning("could not update witness .gitignore: %v", err)
	}

	roleConfig, err := m.roleConfig()
	if err != nil {
		return err
	}

	// Build startup command first
	// NOTE: No gt prime injection needed - SessionStart hook handles it automatically
	// Export GT_ROLE and BD_ACTOR in the command since SetEnvironment only affects new panes
	// Pass m.rig.Path so rig agent settings are honored (not town-level defaults)
	command, err := buildWitnessStartCommand(m.rig.Path, m.rig.Name, townRoot, sessionID, agentOverride, roleConfig)
	if err != nil {
		return err
	}

	// Create session with command directly to avoid send-keys race condition.
	// See: https://github.com/anthropics/gastown/issues/280
	if err := t.NewSessionWithCommand(sessionID, witnessDir, command); err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	// Set environment variables (non-fatal: session works without these)
	// Use centralized AgentEnv for consistency across all role startup paths
	envVars := config.AgentEnv(config.AgentEnvConfig{
		Role:     "witness",
		Rig:      m.rig.Name,
		TownRoot: townRoot,
		Agent:    agentOverride,
	})
	for k, v := range envVars {
		_ = t.SetEnvironment(sessionID, k, v)
	}
	// Apply role config env vars if present (non-fatal).
	for key, value := range roleConfigEnvVars(roleConfig, townRoot, m.rig.Name) {
		_ = t.SetEnvironment(sessionID, key, value)
	}
	// Apply CLI env overrides (highest priority, non-fatal).
	for _, override := range envOverrides {
		if key, value, ok := strings.Cut(override, "="); ok {
			_ = t.SetEnvironment(sessionID, key, value)
		}
	}

	// Apply tmux-specific features (theming, wait-for-command, bypass dialog)
	if extras, ok := t.(session.TmuxExtras); ok {
		theme := tmux.AssignTheme(m.rig.Name)
		_ = extras.ConfigureGasTownSession(sessionID, theme, m.rig.Name, "witness", "witness")

		// Wait for Claude to start - fatal if Claude fails to launch
		if err := extras.WaitForCommand(sessionID, constants.SupportedShells, constants.ClaudeStartTimeout); err != nil {
			_ = t.KillSessionWithProcesses(sessionID)
			return fmt.Errorf("waiting for witness to start: %w", err)
		}

		if err := extras.AcceptBypassPermissionsWarning(sessionID); err != nil {
			log.Printf("warning: accepting bypass permissions for %s: %v", sessionID, err)
		}
	} else {
		// Non-tmux backend: basic wait for session to be ready
		time.Sleep(2 * time.Second)
	}

	// Track PID for defense-in-depth orphan cleanup (tmux-specific, non-fatal)
	if tmuxT, ok := t.(*tmux.Tmux); ok {
		if err := session.TrackSessionPID(townRoot, sessionID, tmuxT); err != nil {
			log.Printf("warning: tracking session PID for %s: %v", sessionID, err)
		}
	}

	time.Sleep(constants.ShutdownNotifyDelay)

	return nil
}

func (m *Manager) roleConfig() (*beads.RoleConfig, error) {
	// Role beads use hq- prefix and live in town-level beads, not rig beads
	townRoot := m.townRoot()
	bd := beads.NewWithBeadsDir(townRoot, beads.ResolveBeadsDir(townRoot))
	roleConfig, err := bd.GetRoleConfig(beads.RoleBeadIDTown("witness"))
	if err != nil {
		return nil, fmt.Errorf("loading witness role config: %w", err)
	}
	return roleConfig, nil
}

func (m *Manager) townRoot() string {
	townRoot, err := workspace.Find(m.rig.Path)
	if err != nil || townRoot == "" {
		return m.rig.Path
	}
	return townRoot
}

func roleConfigEnvVars(roleConfig *beads.RoleConfig, townRoot, rigName string) map[string]string {
	if roleConfig == nil || len(roleConfig.EnvVars) == 0 {
		return nil
	}
	expanded := make(map[string]string, len(roleConfig.EnvVars))
	for key, value := range roleConfig.EnvVars {
		expanded[key] = beads.ExpandRolePattern(value, townRoot, rigName, "", "witness", session.PrefixFor(rigName))
	}
	return expanded
}

func buildWitnessStartCommand(rigPath, rigName, townRoot, sessionName, agentOverride string, roleConfig *beads.RoleConfig) (string, error) {
	if agentOverride != "" {
		roleConfig = nil
	}
	if roleConfig != nil && roleConfig.StartCommand != "" {
		return beads.ExpandRolePattern(roleConfig.StartCommand, townRoot, rigName, "", "witness", session.PrefixFor(rigName)), nil
	}
	initialPrompt := session.BuildStartupPrompt(session.BeaconConfig{
		Recipient: session.BeaconRecipient("witness", "", rigName),
		Sender:    "deacon",
		Topic:     "patrol",
	}, "Run `gt prime --hook` and begin patrol.")
	command, err := config.BuildStartupCommandFromConfig(config.AgentEnvConfig{
		Role:        "witness",
		Rig:         rigName,
		TownRoot:    townRoot,
		Prompt:      initialPrompt,
		Topic:       "patrol",
		SessionName: sessionName,
	}, rigPath, initialPrompt, agentOverride)
	if err != nil {
		return "", fmt.Errorf("building startup command: %w", err)
	}
	return command, nil
}

// Stop stops the witness.
// ZFC-compliant: session existence is the source of truth.
func (m *Manager) Stop() error {
	t := m.backend()
	sessionID := m.SessionName()

	running, _ := t.HasSession(sessionID)
	if !running {
		return ErrNotRunning
	}

	return t.KillSession(sessionID)
}
