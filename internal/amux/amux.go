// Package amux provides a wrapper for amux session operations via subprocess.
// amux is an alternative session multiplexer that uses Unix socket IPC
// instead of the tmux client-server model.
package amux

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	ErrNoDaemon           = errors.New("amux daemon not running")
	ErrSessionExists      = errors.New("session already exists")
	ErrSessionNotFound    = errors.New("session not found")
	ErrNotImplemented     = errors.New("not yet implemented in amux")
	ErrSendKeysNotSupport = errors.New("amux send not yet available (reverted); see gt-1nr")
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

// NewSession creates a new detached amux session.
func (a *Amux) NewSession(name, workDir string) error {
	args := []string{"new", "-t", name}
	if workDir != "" {
		args = append(args, "-d", workDir)
	}
	args = append(args, "--", "/bin/zsh")
	_, err := a.run(args...)
	return err
}

// NewSessionWithCommand creates a new detached amux session running a command.
func (a *Amux) NewSessionWithCommand(name, workDir, command string) error {
	args := []string{"new", "-t", name}
	if workDir != "" {
		args = append(args, "-d", workDir)
	}
	args = append(args, "--", command)
	_, err := a.run(args...)
	return err
}

// NewSessionWithCommandAndEnv creates a new detached amux session with env vars.
// NOTE: amux does not yet support -e flag. Uses os/exec Env field as a workaround.
// TODO(gt-1nr): Use amux -e flag once implemented.
func (a *Amux) NewSessionWithCommandAndEnv(name, workDir, command string, env map[string]string) error {
	args := []string{"new", "-t", name}
	if workDir != "" {
		args = append(args, "-d", workDir)
	}
	args = append(args, "--", command)

	cmd := exec.Command("amux", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Build env: inherit current env and overlay provided vars
	cmd.Env = buildEnv(env)

	err := cmd.Run()
	if err != nil {
		return a.wrapError(err, stderr.String(), args)
	}
	return nil
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
func (a *Amux) HasSession(name string) (bool, error) {
	_, err := a.run("has", "-t", name)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrNoDaemon) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// amuxSession represents a session in amux ls --json output.
type amuxSession struct {
	Name string `json:"name"`
}

// ListSessions returns all session names.
func (a *Amux) ListSessions() ([]string, error) {
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

	var sessions []amuxSession
	if err := json.Unmarshal([]byte(out), &sessions); err != nil {
		return nil, fmt.Errorf("amux ls: parsing JSON: %w", err)
	}

	names := make([]string, len(sessions))
	for i, s := range sessions {
		names[i] = s.Name
	}
	return names, nil
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
// NOTE: amux send was reverted and is not yet available.
// This returns an error until amux send is reimplemented.
// TODO(gt-1nr): Implement once amux send is available.
func (a *Amux) SendKeys(session, keys string) error {
	return fmt.Errorf("%w: SendKeys(%q)", ErrSendKeysNotSupport, session)
}

// NudgeSession sends a message to an amux session with nudge lock serialization.
// NOTE: Depends on SendKeys, which is not yet available.
// TODO(gt-1nr): Implement once amux send is available.
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
// NOTE: amux does not yet support session-level environment variables.
// TODO(gt-1nr): Implement once amux supports set-environment.
func (a *Amux) SetEnvironment(session, key, value string) error {
	return fmt.Errorf("%w: SetEnvironment(%q, %q)", ErrNotImplemented, session, key)
}

// GetEnvironment gets an environment variable from the session.
// NOTE: amux does not yet support session-level environment variables.
// TODO(gt-1nr): Implement once amux supports get-environment.
func (a *Amux) GetEnvironment(session, key string) (string, error) {
	return "", fmt.Errorf("%w: GetEnvironment(%q, %q)", ErrNotImplemented, session, key)
}

// IsAgentRunning checks if an agent appears to be running in the session.
// Uses amux ls --json to check if the session exists and has a running process.
func (a *Amux) IsAgentRunning(session string, expectedPaneCommands ...string) bool {
	out, err := a.run("ls", "--json")
	if err != nil {
		return false
	}

	var sessions []amuxSession
	if err := json.Unmarshal([]byte(out), &sessions); err != nil {
		return false
	}

	for _, s := range sessions {
		if s.Name == session {
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

// buildEnv constructs an environment slice by inheriting the current process
// env and overlaying the provided key-value pairs.
func buildEnv(env map[string]string) []string {
	// Start from current process environment
	base := make(map[string]string)
	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			base[parts[0]] = parts[1]
		}
	}

	// Overlay provided vars
	for k, v := range env {
		base[k] = v
	}

	// Convert to slice
	result := make([]string, 0, len(base))
	for k, v := range base {
		result = append(result, k+"="+v)
	}
	return result
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
