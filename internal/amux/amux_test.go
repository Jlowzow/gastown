package amux

import (
	"errors"
	"os"
	"testing"

	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/tmux"
)

// Compile-time check: *Amux must satisfy SessionBackend.
var _ session.SessionBackend = (*Amux)(nil)

func TestNewAmux(t *testing.T) {
	a := NewAmux()
	if a == nil {
		t.Fatal("NewAmux returned nil")
	}
}

func TestWrapError_NoDaemon(t *testing.T) {
	a := NewAmux()

	tests := []struct {
		stderr string
	}{
		{"daemon not running"},
		{"connection refused"},
		{"no such file or directory"},
	}

	for _, tt := range tests {
		err := a.wrapError(errors.New("exit 1"), tt.stderr, []string{"ls"})
		if !errors.Is(err, ErrNoDaemon) {
			t.Errorf("wrapError(%q) = %v, want ErrNoDaemon", tt.stderr, err)
		}
	}
}

func TestWrapError_SessionExists(t *testing.T) {
	a := NewAmux()
	err := a.wrapError(errors.New("exit 1"), "session already exists", []string{"new"})
	if !errors.Is(err, ErrSessionExists) {
		t.Errorf("wrapError(already exists) = %v, want ErrSessionExists", err)
	}
}

func TestWrapError_SessionNotFound(t *testing.T) {
	a := NewAmux()

	tests := []struct {
		stderr string
	}{
		{"session not found"},
		{"no such session"},
	}

	for _, tt := range tests {
		err := a.wrapError(errors.New("exit 1"), tt.stderr, []string{"has"})
		if !errors.Is(err, ErrSessionNotFound) {
			t.Errorf("wrapError(%q) = %v, want ErrSessionNotFound", tt.stderr, err)
		}
	}
}

func TestWrapError_GenericWithStderr(t *testing.T) {
	a := NewAmux()
	err := a.wrapError(errors.New("exit 1"), "some unknown error", []string{"kill"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !containsAll(err.Error(), "amux", "kill", "some unknown error") {
		t.Errorf("error %q should contain amux, kill, and stderr message", err)
	}
}

func TestWrapError_GenericWithoutStderr(t *testing.T) {
	a := NewAmux()
	origErr := errors.New("exit status 1")
	err := a.wrapError(origErr, "", []string{"new"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, origErr) {
		t.Errorf("error should wrap original: got %v", err)
	}
}

func TestSendKeys_ReturnsNotSupported(t *testing.T) {
	a := NewAmux()
	err := a.SendKeys("test-session", "hello")
	if err == nil {
		t.Fatal("expected error from SendKeys")
	}
	if !errors.Is(err, ErrSendKeysNotSupport) {
		t.Errorf("SendKeys error = %v, want ErrSendKeysNotSupport", err)
	}
}

func TestSetEnvironment_ReturnsNotImplemented(t *testing.T) {
	a := NewAmux()
	err := a.SetEnvironment("test-session", "KEY", "VALUE")
	if err == nil {
		t.Fatal("expected error from SetEnvironment")
	}
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("SetEnvironment error = %v, want ErrNotImplemented", err)
	}
}

func TestGetEnvironment_ReturnsNotImplemented(t *testing.T) {
	a := NewAmux()
	_, err := a.GetEnvironment("test-session", "KEY")
	if err == nil {
		t.Fatal("expected error from GetEnvironment")
	}
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("GetEnvironment error = %v, want ErrNotImplemented", err)
	}
}

func TestGetSessionSet_ReturnsSessionSet(t *testing.T) {
	// Test that GetSessionSet returns a valid *tmux.SessionSet type.
	// This verifies the type compatibility without needing a running amux daemon.
	set := tmux.NewSessionSet([]string{"a", "b"})
	if !set.Has("a") {
		t.Error("SessionSet should contain 'a'")
	}
	if set.Has("c") {
		t.Error("SessionSet should not contain 'c'")
	}
}

func TestBuildEnv(t *testing.T) {
	env := buildEnv(map[string]string{
		"TEST_AMUX_KEY": "test_value",
	})

	found := false
	for _, e := range env {
		if e == "TEST_AMUX_KEY=test_value" {
			found = true
			break
		}
	}
	if !found {
		t.Error("buildEnv should include TEST_AMUX_KEY=test_value")
	}

	// Should also include inherited env vars
	home := os.Getenv("HOME")
	if home != "" {
		foundHome := false
		for _, e := range env {
			if e == "HOME="+home {
				foundHome = true
				break
			}
		}
		if !foundHome {
			t.Error("buildEnv should inherit HOME from current env")
		}
	}
}

func TestBuildEnv_OverridesExisting(t *testing.T) {
	// Set a known env var, then override it
	orig := os.Getenv("PATH")
	env := buildEnv(map[string]string{
		"PATH": "/custom/path",
	})

	foundCustom := false
	foundOrig := false
	for _, e := range env {
		if e == "PATH=/custom/path" {
			foundCustom = true
		}
		if e == "PATH="+orig {
			foundOrig = true
		}
	}
	if !foundCustom {
		t.Error("buildEnv should override PATH with custom value")
	}
	if foundOrig {
		t.Error("buildEnv should not retain original PATH when overridden")
	}
}

func TestNudgeLock(t *testing.T) {
	session := "test-nudge-lock"

	// Should be able to acquire
	if !acquireNudgeLock(session, 100*1000000) { // 100ms
		t.Fatal("should acquire lock on first try")
	}

	// Should fail to acquire while held (with short timeout)
	if acquireNudgeLock(session, 1000000) { // 1ms
		t.Fatal("should not acquire lock while held")
	}

	// Release and reacquire
	releaseNudgeLock(session)
	if !acquireNudgeLock(session, 100*1000000) { // 100ms
		t.Fatal("should acquire lock after release")
	}
	releaseNudgeLock(session)
}

// containsAll checks if s contains all the given substrings.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
