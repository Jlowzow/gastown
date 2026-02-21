//go:build integration

package amux

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// ensureDaemon starts the amux daemon if not already running.
// Returns a cleanup function that stops the daemon if we started it.
func ensureDaemon(t *testing.T) func() {
	t.Helper()

	// Check if daemon is already running
	cmd := exec.Command("amux", "ping")
	if err := cmd.Run(); err == nil {
		// Already running, no cleanup needed
		return func() {}
	}

	// Start daemon
	startCmd := exec.Command("amux", "start-server")
	if err := startCmd.Start(); err != nil {
		t.Fatalf("failed to start amux daemon: %v", err)
	}

	// Wait for daemon to be ready
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		pingCmd := exec.Command("amux", "ping")
		if err := pingCmd.Run(); err == nil {
			return func() {
				exec.Command("amux", "kill-server", "--force").Run()
			}
		}
	}
	t.Fatal("amux daemon did not become ready within 2s")
	return func() {}
}

// uniqueSession returns a test-scoped session name to avoid collisions.
func uniqueSession(t *testing.T, suffix string) string {
	t.Helper()
	// Use test name + suffix for uniqueness
	name := fmt.Sprintf("test-%s-%s-%d", t.Name(), suffix, time.Now().UnixNano()%10000)
	// amux session names may have length limits; truncate if needed
	if len(name) > 60 {
		name = name[:60]
	}
	return name
}

func TestIntegration_SessionLifecycle(t *testing.T) {
	cleanup := ensureDaemon(t)
	defer cleanup()

	a := NewAmux()
	session := uniqueSession(t, "lifecycle")

	// 1. Create session
	err := a.NewSession(session, t.TempDir())
	if err != nil {
		t.Fatalf("NewSession(%q) failed: %v", session, err)
	}
	defer a.KillSession(session) // cleanup

	// 2. HasSession should return true
	exists, err := a.HasSession(session)
	if err != nil {
		t.Fatalf("HasSession(%q) failed: %v", session, err)
	}
	if !exists {
		t.Fatalf("HasSession(%q) = false, want true", session)
	}

	// 3. Session should appear in ListSessions
	sessions, err := a.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() failed: %v", err)
	}
	found := false
	for _, s := range sessions {
		if s == session {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ListSessions() = %v, want to contain %q", sessions, session)
	}

	// 4. Session should appear in GetSessionSet
	set, err := a.GetSessionSet()
	if err != nil {
		t.Fatalf("GetSessionSet() failed: %v", err)
	}
	if !set.Has(session) {
		t.Fatalf("GetSessionSet().Has(%q) = false, want true", session)
	}

	// 5. IsAgentAlive should return true
	if !a.IsAgentAlive(session) {
		t.Fatalf("IsAgentAlive(%q) = false, want true", session)
	}

	// 6. IsAgentRunning should return true
	if !a.IsAgentRunning(session) {
		t.Fatalf("IsAgentRunning(%q) = false, want true", session)
	}

	// 7. IsAvailable should return true
	if !a.IsAvailable() {
		t.Fatal("IsAvailable() = false, want true")
	}

	// 8. Kill session
	err = a.KillSession(session)
	if err != nil {
		t.Fatalf("KillSession(%q) failed: %v", session, err)
	}

	// 9. HasSession should return false after kill
	exists, err = a.HasSession(session)
	if err != nil {
		t.Fatalf("HasSession(%q) after kill failed: %v", session, err)
	}
	if exists {
		t.Fatalf("HasSession(%q) = true after kill, want false", session)
	}

	// 10. IsAgentAlive should return false after kill
	if a.IsAgentAlive(session) {
		t.Fatalf("IsAgentAlive(%q) = true after kill, want false", session)
	}
}

func TestIntegration_NewSessionWithCommand(t *testing.T) {
	cleanup := ensureDaemon(t)
	defer cleanup()

	a := NewAmux()
	session := uniqueSession(t, "cmd")

	err := a.NewSessionWithCommand(session, t.TempDir(), "echo hello")
	if err != nil {
		t.Fatalf("NewSessionWithCommand(%q) failed: %v", session, err)
	}
	defer a.KillSession(session)

	exists, err := a.HasSession(session)
	if err != nil {
		t.Fatalf("HasSession(%q) failed: %v", session, err)
	}
	// Session may or may not still exist (echo exits immediately),
	// but creation should have succeeded without error.
	_ = exists
}

func TestIntegration_NewSessionWithCommandAndEnv(t *testing.T) {
	cleanup := ensureDaemon(t)
	defer cleanup()

	a := NewAmux()
	session := uniqueSession(t, "env")

	env := map[string]string{
		"GT_ROLE":  "gastown/polecats/test",
		"GT_RIG":   "gastown",
		"GT_AGENT": "test-agent",
	}

	// Create session with env vars and a long-running command
	err := a.NewSessionWithCommandAndEnv(session, t.TempDir(), "/bin/sleep 30", env)
	if err != nil {
		t.Fatalf("NewSessionWithCommandAndEnv(%q) failed: %v", session, err)
	}
	defer a.KillSession(session)

	exists, err := a.HasSession(session)
	if err != nil {
		t.Fatalf("HasSession(%q) failed: %v", session, err)
	}
	if !exists {
		t.Fatalf("HasSession(%q) = false after create with env, want true", session)
	}
}

func TestIntegration_SendKeysAndCapture(t *testing.T) {
	cleanup := ensureDaemon(t)
	defer cleanup()

	a := NewAmux()
	session := uniqueSession(t, "send")

	// Create a shell session
	err := a.NewSession(session, t.TempDir())
	if err != nil {
		t.Fatalf("NewSession(%q) failed: %v", session, err)
	}
	defer a.KillSession(session)

	// Give the shell a moment to initialize
	time.Sleep(500 * time.Millisecond)

	// Send a command that produces recognizable output
	err = a.SendKeys(session, "echo AMUX_TEST_MARKER_12345\n")
	if err != nil {
		t.Fatalf("SendKeys(%q) failed: %v", session, err)
	}

	// Wait for command to execute
	time.Sleep(500 * time.Millisecond)

	// Capture output and verify
	output, err := a.CapturePane(session, 50)
	if err != nil {
		t.Fatalf("CapturePane(%q) failed: %v", session, err)
	}

	if !strings.Contains(output, "AMUX_TEST_MARKER_12345") {
		t.Errorf("CapturePane output does not contain marker.\nGot: %s", output)
	}
}

func TestIntegration_NudgeSession(t *testing.T) {
	cleanup := ensureDaemon(t)
	defer cleanup()

	a := NewAmux()
	session := uniqueSession(t, "nudge")

	err := a.NewSession(session, t.TempDir())
	if err != nil {
		t.Fatalf("NewSession(%q) failed: %v", session, err)
	}
	defer a.KillSession(session)

	time.Sleep(500 * time.Millisecond)

	// NudgeSession should succeed (delegates to SendKeys)
	err = a.NudgeSession(session, "echo NUDGE_TEST\n")
	if err != nil {
		t.Fatalf("NudgeSession(%q) failed: %v", session, err)
	}
}

func TestIntegration_EnvironmentSetGet(t *testing.T) {
	cleanup := ensureDaemon(t)
	defer cleanup()

	a := NewAmux()
	session := uniqueSession(t, "envsg")

	err := a.NewSession(session, t.TempDir())
	if err != nil {
		t.Fatalf("NewSession(%q) failed: %v", session, err)
	}
	defer a.KillSession(session)

	// Set an environment variable
	err = a.SetEnvironment(session, "GT_TEST_KEY", "test_value_42")
	if err != nil {
		t.Fatalf("SetEnvironment(%q, GT_TEST_KEY) failed: %v", session, err)
	}

	// Get it back
	val, err := a.GetEnvironment(session, "GT_TEST_KEY")
	if err != nil {
		t.Fatalf("GetEnvironment(%q, GT_TEST_KEY) failed: %v", session, err)
	}
	if val != "test_value_42" {
		t.Errorf("GetEnvironment(%q, GT_TEST_KEY) = %q, want %q", session, val, "test_value_42")
	}

	// Set GT-specific metadata vars
	gtVars := map[string]string{
		"GT_ROLE":  "gastown/polecats/furiosa",
		"GT_RIG":   "gastown",
		"GT_AGENT": "furiosa",
	}
	for k, v := range gtVars {
		if err := a.SetEnvironment(session, k, v); err != nil {
			t.Fatalf("SetEnvironment(%q, %s) failed: %v", session, k, err)
		}
	}
	for k, want := range gtVars {
		got, err := a.GetEnvironment(session, k)
		if err != nil {
			t.Fatalf("GetEnvironment(%q, %s) failed: %v", session, k, err)
		}
		if got != want {
			t.Errorf("GetEnvironment(%q, %s) = %q, want %q", session, k, got, want)
		}
	}
}

func TestIntegration_KillSessionWithProcesses(t *testing.T) {
	cleanup := ensureDaemon(t)
	defer cleanup()

	a := NewAmux()
	session := uniqueSession(t, "killp")

	err := a.NewSessionWithCommand(session, t.TempDir(), "/bin/sleep 300")
	if err != nil {
		t.Fatalf("NewSessionWithCommand(%q) failed: %v", session, err)
	}

	// Verify it exists
	exists, err := a.HasSession(session)
	if err != nil {
		t.Fatalf("HasSession(%q) failed: %v", session, err)
	}
	if !exists {
		t.Fatalf("HasSession(%q) = false, want true", session)
	}

	// KillSessionWithProcesses (equivalent to KillSession in amux)
	err = a.KillSessionWithProcesses(session)
	if err != nil {
		t.Fatalf("KillSessionWithProcesses(%q) failed: %v", session, err)
	}

	// Verify cleanup
	exists, err = a.HasSession(session)
	if err != nil {
		t.Fatalf("HasSession after kill failed: %v", err)
	}
	if exists {
		t.Fatalf("HasSession(%q) = true after KillSessionWithProcesses, want false", session)
	}
}

func TestIntegration_DuplicateSession(t *testing.T) {
	cleanup := ensureDaemon(t)
	defer cleanup()

	a := NewAmux()
	session := uniqueSession(t, "dup")

	err := a.NewSession(session, t.TempDir())
	if err != nil {
		t.Fatalf("NewSession(%q) failed: %v", session, err)
	}
	defer a.KillSession(session)

	// Creating same session again should fail
	err = a.NewSession(session, t.TempDir())
	if err == nil {
		t.Fatal("creating duplicate session should fail")
	}
}

func TestIntegration_HasSession_Nonexistent(t *testing.T) {
	cleanup := ensureDaemon(t)
	defer cleanup()

	a := NewAmux()

	exists, err := a.HasSession("nonexistent-session-xyz")
	if err != nil {
		t.Fatalf("HasSession(nonexistent) failed: %v", err)
	}
	if exists {
		t.Fatal("HasSession(nonexistent) = true, want false")
	}
}

// TestIntegration_GTPolecat_EndToEnd simulates a complete GT polecat session
// lifecycle on the amux backend. This validates the full SessionBackend interface
// works end-to-end before switching GT over from tmux.
//
// The test follows the exact sequence GT uses when managing a polecat:
// 1. Create session with GT env vars (GT_ROLE, GT_RIG, GT_AGENT)
// 2. Verify session appears in listings
// 3. Set runtime metadata via SetEnvironment (hook_bead, session_id)
// 4. Read metadata back via GetEnvironment
// 5. Send a command via SendKeys (simulating a nudge)
// 6. Capture output and verify
// 7. Kill session and verify complete cleanup
func TestIntegration_GTPolecat_EndToEnd(t *testing.T) {
	cleanup := ensureDaemon(t)
	defer cleanup()

	a := NewAmux()
	session := uniqueSession(t, "gt-e2e")
	workDir := t.TempDir()

	// === Phase 1: Create session with GT env vars ===
	// This is how GT spawns a polecat: with role, rig, and agent identity.
	gtEnv := map[string]string{
		"GT_ROLE":  "gastown/polecats/furiosa",
		"GT_RIG":   "gastown",
		"GT_AGENT": "furiosa",
	}

	// Use a shell that stays alive (simulating a polecat running claude)
	err := a.NewSessionWithCommandAndEnv(session, workDir, "/bin/sh", gtEnv)
	if err != nil {
		t.Fatalf("Phase 1: NewSessionWithCommandAndEnv(%q) failed: %v", session, err)
	}
	defer a.KillSession(session)

	// === Phase 2: Verify session appears in listings ===
	// GT checks HasSession and ListSessions to verify spawns succeeded.
	exists, err := a.HasSession(session)
	if err != nil {
		t.Fatalf("Phase 2: HasSession(%q) failed: %v", session, err)
	}
	if !exists {
		t.Fatalf("Phase 2: HasSession(%q) = false, want true", session)
	}

	sessions, err := a.ListSessions()
	if err != nil {
		t.Fatalf("Phase 2: ListSessions() failed: %v", err)
	}
	found := false
	for _, s := range sessions {
		if s == session {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Phase 2: session %q not found in ListSessions() = %v", session, sessions)
	}

	set, err := a.GetSessionSet()
	if err != nil {
		t.Fatalf("Phase 2: GetSessionSet() failed: %v", err)
	}
	if !set.Has(session) {
		t.Fatalf("Phase 2: GetSessionSet().Has(%q) = false", session)
	}

	if !a.IsAgentAlive(session) {
		t.Fatalf("Phase 2: IsAgentAlive(%q) = false", session)
	}
	if !a.IsAgentRunning(session) {
		t.Fatalf("Phase 2: IsAgentRunning(%q) = false", session)
	}

	// === Phase 3: Set runtime metadata via SetEnvironment ===
	// GT uses session env to store runtime state like hook_bead and session_id.
	metadata := map[string]string{
		"GT_HOOK_BEAD":  "gt-asi",
		"GT_SESSION_ID": "e2e-test-session-001",
		"GT_MOLECULE":   "gt-wisp-ull",
	}
	for k, v := range metadata {
		if err := a.SetEnvironment(session, k, v); err != nil {
			t.Fatalf("Phase 3: SetEnvironment(%q, %s, %s) failed: %v", session, k, v, err)
		}
	}

	// === Phase 4: Read metadata back via GetEnvironment ===
	for k, want := range metadata {
		got, err := a.GetEnvironment(session, k)
		if err != nil {
			t.Fatalf("Phase 4: GetEnvironment(%q, %s) failed: %v", session, k, err)
		}
		if got != want {
			t.Errorf("Phase 4: GetEnvironment(%q, %s) = %q, want %q", session, k, got, want)
		}
	}

	// === Phase 5: Send a command via SendKeys ===
	// GT nudges polecats by sending text to their session.
	time.Sleep(300 * time.Millisecond) // let shell initialize

	marker := "GT_E2E_POLECAT_MARKER"
	err = a.SendKeys(session, fmt.Sprintf("echo %s\n", marker))
	if err != nil {
		t.Fatalf("Phase 5: SendKeys(%q) failed: %v", session, err)
	}

	// === Phase 6: Capture output and verify ===
	time.Sleep(500 * time.Millisecond) // let command execute

	output, err := a.CapturePane(session, 50)
	if err != nil {
		t.Fatalf("Phase 6: CapturePane(%q) failed: %v", session, err)
	}
	if !strings.Contains(output, marker) {
		t.Errorf("Phase 6: CapturePane output missing marker %q.\nGot: %s", marker, output)
	}

	// Also test NudgeSession (which GT uses for serialized message delivery)
	nudgeMarker := "GT_E2E_NUDGE_MARKER"
	err = a.NudgeSession(session, fmt.Sprintf("echo %s\n", nudgeMarker))
	if err != nil {
		t.Fatalf("Phase 6: NudgeSession(%q) failed: %v", session, err)
	}
	time.Sleep(500 * time.Millisecond)

	output, err = a.CapturePane(session, 50)
	if err != nil {
		t.Fatalf("Phase 6: CapturePane after nudge failed: %v", err)
	}
	if !strings.Contains(output, nudgeMarker) {
		t.Errorf("Phase 6: CapturePane output missing nudge marker %q.\nGot: %s", nudgeMarker, output)
	}

	// === Phase 7: Kill session and verify complete cleanup ===
	err = a.KillSession(session)
	if err != nil {
		t.Fatalf("Phase 7: KillSession(%q) failed: %v", session, err)
	}

	// Verify HasSession returns false
	exists, err = a.HasSession(session)
	if err != nil {
		t.Fatalf("Phase 7: HasSession after kill failed: %v", err)
	}
	if exists {
		t.Fatalf("Phase 7: HasSession(%q) = true after kill", session)
	}

	// Verify IsAgentAlive returns false
	if a.IsAgentAlive(session) {
		t.Fatalf("Phase 7: IsAgentAlive(%q) = true after kill", session)
	}

	// Verify IsAgentRunning returns false
	if a.IsAgentRunning(session) {
		t.Fatalf("Phase 7: IsAgentRunning(%q) = true after kill", session)
	}

	// Verify session no longer appears in listings
	sessions, err = a.ListSessions()
	if err != nil {
		t.Fatalf("Phase 7: ListSessions after kill failed: %v", err)
	}
	for _, s := range sessions {
		if s == session {
			t.Fatalf("Phase 7: session %q still in ListSessions after kill", session)
		}
	}
}

func TestIntegration_CapturePane_Nonexistent(t *testing.T) {
	cleanup := ensureDaemon(t)
	defer cleanup()

	a := NewAmux()

	out, err := a.CapturePane("nonexistent-session-xyz", 10)
	// amux capture may return empty output or an error for nonexistent sessions.
	// Either behavior is acceptable as long as it doesn't panic.
	if err != nil {
		// Expected: error for nonexistent session
		return
	}
	// If no error, output should be empty
	if out != "" {
		t.Errorf("CapturePane on nonexistent session returned unexpected output: %q", out)
	}
}
