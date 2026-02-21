// Package amux provides a wrapper for amux session operations via subprocess.
// amux is an alternative session multiplexer that uses Unix socket IPC
// instead of the tmux client-server model.
package amux

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/steveyegge/gastown/internal/tmux"
)

// nudgeLockTimeout is how long to wait to acquire the per-session nudge lock.
const nudgeLockTimeout = 30 * time.Second

// sessionNudgeLocks serializes nudges to the same session.
var sessionNudgeLocks sync.Map // map[string]chan struct{}

// Common errors
var (
	ErrNoDaemon        = errors.New("amux daemon not running")
	ErrSessionExists   = errors.New("session already exists")
	ErrSessionNotFound = errors.New("session not found")
)

// Amux wraps amux CLI operations, implementing the SessionBackend interface.
type Amux struct{}

// NewAmux creates a new Amux wrapper.
func NewAmux() *Amux {
	return &Amux{}
}

// run executes an amux command and returns stdout.
func (a *Amux) run(args ...string) (string, error) {
	cmd := exec.Command("amux", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", a.wrapError(err, stderr.String(), args)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// wrapError wraps amux errors with context, mapping amux-specific messages
// to well-known error types.
func (a *Amux) wrapError(err error, stderr string, args []string) error {
	stderr = strings.TrimSpace(stderr)

	// Map amux error messages to common error types
	if strings.Contains(stderr, "daemon not running") ||
		strings.Contains(stderr, "connection refused") ||
		strings.Contains(stderr, "no such file or directory") {
		return ErrNoDaemon
	}
	if strings.Contains(stderr, "already exists") {
		return ErrSessionExists
	}
	if strings.Contains(stderr, "not found") ||
		strings.Contains(stderr, "no such session") {
		return ErrSessionNotFound
	}

	if stderr != "" {
		return fmt.Errorf("amux %s: %s", args[0], stderr)
	}
	return fmt.Errorf("amux %s: %w", args[0], err)
}

// wrapCommand prepends a cd to workDir if provided, wrapping the command in a shell.
func wrapCommand(workDir, command string) string {
	if workDir == "" {
		return command
	}
	return fmt.Sprintf("cd %s && exec %s", shellQuote(workDir), command)
}

// shellQuote wraps a string in single quotes for safe shell expansion.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// NewSession creates a new detached amux session.
func (a *Amux) NewSession(name, workDir string) error {
	cmd := wrapCommand(workDir, "/bin/zsh")
	args := []string{"new", "-d", "-t", name, "--", "/bin/sh", "-c", cmd}
	_, err := a.run(args...)
	return err
}

// NewSessionWithCommand creates a new detached amux session running a command.
func (a *Amux) NewSessionWithCommand(name, workDir, command string) error {
	cmd := wrapCommand(workDir, command)
	args := []string{"new", "-d", "-t", name, "--", "/bin/sh", "-c", cmd}
	_, err := a.run(args...)
	return err
}

// NewSessionWithCommandAndEnv creates a new detached amux session with env vars.
func (a *Amux) NewSessionWithCommandAndEnv(name, workDir, command string, env map[string]string) error {
	cmd := wrapCommand(workDir, command)
	args := []string{"new", "-d", "-t", name}
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, "--", "/bin/sh", "-c", cmd)
	_, err := a.run(args...)
	return err
}

// KillSession terminates an amux session.
// amux kill terminates the process tree by default.
func (a *Amux) KillSession(name string) error {
	_, err := a.run("kill", "-t", name)
	return err
}

// KillSessionWithProcesses terminates an amux session and all its processes.
// amux kill already terminates the entire process tree by default,
// so this is equivalent to KillSession.
func (a *Amux) KillSessionWithProcesses(name string) error {
	return a.KillSession(name)
}

// HasSession checks if a session exists.
// amux has exits 0 if the session exists, non-zero otherwise.
func (a *Amux) HasSession(name string) (bool, error) {
	_, err := a.run("has", "-t", name)
	if err != nil {
		// amux has returns exit 1 for nonexistent sessions (no stderr).
		// Treat any error as "not found" rather than propagating.
		return false, nil
	}
	return true, nil
}

// SessionInfo represents the full session detail from amux ls/info --json.
type SessionInfo struct {
	Name         string  `json:"name"`
	Command      string  `json:"command"`
	PID          int     `json:"pid"`
	Alive        bool    `json:"alive"`
	CreatedAt    string  `json:"created_at"`
	UptimeSecs   float64 `json:"uptime_secs"`
	LastActivity string  `json:"last_activity"`
	IdleSecs     float64 `json:"idle_secs"`
}

// ListSessionInfo returns full session info for all sessions.
func (a *Amux) ListSessionInfo() ([]SessionInfo, error) {
	out, err := a.run("ls", "--json")
	if err != nil {
		if errors.Is(err, ErrNoDaemon) {
			return nil, nil
		}
		return nil, err
	}

	if out == "" {
		return nil, nil
	}

	var sessions []SessionInfo
	if err := json.Unmarshal([]byte(out), &sessions); err != nil {
		return nil, fmt.Errorf("amux ls: parsing JSON: %w", err)
	}
	return sessions, nil
}

// ListSessions returns all session names.
func (a *Amux) ListSessions() ([]string, error) {
	sessions, err := a.ListSessionInfo()
	if err != nil {
		return nil, err
	}
	names := make([]string, len(sessions))
	for i, s := range sessions {
		names[i] = s.Name
	}
	return names, nil
}

// GetSessionInfo returns detailed info for a single session.
func (a *Amux) GetSessionInfo(name string) (*SessionInfo, error) {
	out, err := a.run("info", "-t", name, "--json")
	if err != nil {
		return nil, err
	}

	var info SessionInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return nil, fmt.Errorf("amux info: parsing JSON: %w", err)
	}
	return &info, nil
}

// GetSessionSet returns a SessionSet containing all current sessions.
func (a *Amux) GetSessionSet() (*tmux.SessionSet, error) {
	names, err := a.ListSessions()
	if err != nil {
		return nil, err
	}
	return tmux.NewSessionSet(names), nil
}

// SendKeys sends keystrokes to a session.
func (a *Amux) SendKeys(session, keys string) error {
	_, err := a.run("send", "-t", session, "-l", keys)
	return err
}

// NudgeSession sends a message to an amux session with nudge lock serialization.
func (a *Amux) NudgeSession(session, message string) error {
	// Serialize nudges to this session to prevent interleaving.
	if !acquireNudgeLock(session, nudgeLockTimeout) {
		return fmt.Errorf("nudge lock timeout for session %q: previous nudge may be hung", session)
	}
	defer releaseNudgeLock(session)

	return a.SendKeys(session, message)
}

// CapturePane captures the last N lines of a session's output.
func (a *Amux) CapturePane(session string, lines int) (string, error) {
	return a.run("capture", "-t", session, "--lines", fmt.Sprintf("%d", lines))
}

// SetEnvironment sets an environment variable in the session.
func (a *Amux) SetEnvironment(session, key, value string) error {
	_, err := a.run("env", "set", "-t", session, key, value)
	return err
}

// GetEnvironment gets an environment variable from the session.
func (a *Amux) GetEnvironment(session, key string) (string, error) {
	return a.run("env", "get", "-t", session, key)
}

// IsAgentRunning checks if an agent appears to be running in the session.
// Uses amux info to check session existence, process liveness, and optionally
// whether the command matches expectedPaneCommands.
func (a *Amux) IsAgentRunning(session string, expectedPaneCommands ...string) bool {
	info, err := a.GetSessionInfo(session)
	if err != nil {
		return false
	}
	if !info.Alive {
		return false
	}
	// If no expected commands specified, alive is sufficient.
	if len(expectedPaneCommands) == 0 {
		return true
	}
	// Check if the session's command contains any of the expected commands.
	for _, cmd := range expectedPaneCommands {
		if strings.Contains(info.Command, cmd) {
			return true
		}
	}
	return false
}

// IsAgentAlive checks if a session exists.
func (a *Amux) IsAgentAlive(session string) bool {
	exists, err := a.HasSession(session)
	return err == nil && exists
}

// IsAvailable checks if the amux daemon is running and responsive.
func (a *Amux) IsAvailable() bool {
	_, err := a.run("ls")
	return err == nil
}

// CheckSessionHealth determines the health status of an amux session.
// Uses amux info --json to check process liveness and idle time.
func (a *Amux) CheckSessionHealth(session string, maxInactivity time.Duration) tmux.ZombieStatus {
	info, err := a.GetSessionInfo(session)
	if err != nil {
		return tmux.SessionDead
	}
	if !info.Alive {
		return tmux.AgentDead
	}
	if maxInactivity > 0 && time.Duration(info.IdleSecs*float64(time.Second)) > maxInactivity {
		return tmux.AgentHung
	}
	return tmux.SessionHealthy
}

// SendKeysRaw sends literal text to a session (amux send -l).
// In amux, SendKeys already uses -l (literal mode), so this is equivalent.
func (a *Amux) SendKeysRaw(session, keys string) error {
	return a.SendKeys(session, keys)
}

// AttachSession attaches to an existing amux session.
func (a *Amux) AttachSession(session string) error {
	cmd := exec.Command("amux", "attach", "-t", session)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

// WaitForExit blocks until the session exits or timeout is reached.
// Returns nil if the session exited, or an error on timeout.
func (a *Amux) WaitForExit(session string, timeout time.Duration) error {
	args := []string{"wait", "-t", session}
	if timeout > 0 {
		args = append(args, "--timeout", fmt.Sprintf("%d", int(timeout.Seconds())))
	}
	_, err := a.run(args...)
	return err
}

// getSessionNudgeSem returns the channel semaphore for serializing nudges.
func getSessionNudgeSem(session string) chan struct{} {
	sem := make(chan struct{}, 1)
	actual, _ := sessionNudgeLocks.LoadOrStore(session, sem)
	return actual.(chan struct{})
}

// acquireNudgeLock attempts to acquire the per-session nudge lock with a timeout.
func acquireNudgeLock(session string, timeout time.Duration) bool {
	sem := getSessionNudgeSem(session)
	select {
	case sem <- struct{}{}:
		return true
	case <-time.After(timeout):
		return false
	}
}

// releaseNudgeLock releases the per-session nudge lock.
func releaseNudgeLock(session string) {
	sem := getSessionNudgeSem(session)
	select {
	case <-sem:
	default:
	}
}
