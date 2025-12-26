package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/wisp"
)

// Resume command checks for cleared gates and resumes parked work.

var resumeCmd = &cobra.Command{
	Use:     "resume",
	GroupID: GroupWork,
	Short:   "Resume from parked work when gate clears",
	Long: `Resume work that was parked on a gate.

This command checks if you have parked work and whether its gate has cleared.
If the gate is closed, it restores your work context so you can continue.

The resume command:
  1. Checks for parked work state
  2. Verifies the gate has closed
  3. Restores the hook with your previous work
  4. Displays context notes to help you continue

Examples:
  gt resume              # Check for and resume parked work
  gt resume --status     # Just show parked work status without resuming`,
	RunE: runResume,
}

var (
	resumeStatusOnly bool
	resumeJSON       bool
)

func init() {
	resumeCmd.Flags().BoolVar(&resumeStatusOnly, "status", false, "Just show parked work status")
	resumeCmd.Flags().BoolVar(&resumeJSON, "json", false, "Output as JSON")
	rootCmd.AddCommand(resumeCmd)
}

// ResumeStatus represents the current resume state.
type ResumeStatus struct {
	HasParkedWork bool        `json:"has_parked_work"`
	ParkedWork    *ParkedWork `json:"parked_work,omitempty"`
	GateClosed    bool        `json:"gate_closed"`
	CloseReason   string      `json:"close_reason,omitempty"`
	CanResume     bool        `json:"can_resume"`
}

func runResume(cmd *cobra.Command, args []string) error {
	// Detect agent identity
	agentID, _, cloneRoot, err := resolveSelfTarget()
	if err != nil {
		return fmt.Errorf("detecting agent identity: %w", err)
	}

	// Check for parked work
	parked, err := readParkedWork(cloneRoot, agentID)
	if err != nil {
		return fmt.Errorf("reading parked work: %w", err)
	}

	status := ResumeStatus{
		HasParkedWork: parked != nil,
		ParkedWork:    parked,
	}

	if parked == nil {
		if resumeJSON {
			return outputResumeStatus(status)
		}
		fmt.Printf("%s No parked work found\n", style.Dim.Render("○"))
		fmt.Printf("  Use 'gt park <gate-id>' to park work on a gate\n")
		return nil
	}

	// Check gate status
	gateCheck := exec.Command("bd", "gate", "show", parked.GateID, "--json")
	gateOutput, err := gateCheck.Output()
	gateNotFound := false
	if err != nil {
		// Gate might have been deleted (wisp cleanup) or is inaccessible
		// Treat as "gate gone" - allow clearing stale parked work
		gateNotFound = true
		status.GateClosed = true // Treat as closed so user can clear it
		status.CloseReason = "Gate no longer exists (may have been cleaned up)"
	} else {
		var gateInfo struct {
			ID          string `json:"id"`
			Status      string `json:"status"`
			CloseReason string `json:"close_reason"`
		}
		if err := json.Unmarshal(gateOutput, &gateInfo); err == nil {
			status.GateClosed = gateInfo.Status == "closed"
			status.CloseReason = gateInfo.CloseReason
		}
	}

	status.CanResume = status.GateClosed

	// Status-only mode
	if resumeStatusOnly {
		if resumeJSON {
			return outputResumeStatus(status)
		}
		return displayResumeStatus(status, parked)
	}

	// JSON output
	if resumeJSON {
		return outputResumeStatus(status)
	}

	// If gate not closed yet, show status and exit
	if !status.GateClosed {
		fmt.Printf("%s Work parked on gate %s (still open)\n",
			style.Bold.Render("🅿️"), parked.GateID)
		if parked.BeadID != "" {
			fmt.Printf("  Working on: %s\n", parked.BeadID)
		}
		fmt.Printf("  Parked at: %s\n", parked.ParkedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("\n%s Gate still open. Check back later or run 'bd gate show %s'\n",
			style.Dim.Render("⏳"), parked.GateID)
		return nil
	}

	// Gate closed - resume work!
	if gateNotFound {
		fmt.Printf("%s Gate %s no longer exists\n", style.Bold.Render("⚠️"), parked.GateID)
		fmt.Printf("  The gate may have been cleaned up. Restoring parked work anyway.\n")
	} else {
		fmt.Printf("%s Gate %s has cleared!\n", style.Bold.Render("🚦"), parked.GateID)
		if status.CloseReason != "" {
			fmt.Printf("  Reason: %s\n", status.CloseReason)
		}
	}

	// Restore hook if we have a bead
	if parked.BeadID != "" {
		hook := wisp.NewSlungWork(parked.BeadID, agentID)
		hook.Formula = parked.Formula
		hook.Context = parked.Context

		if err := wisp.WriteSlungWork(cloneRoot, agentID, hook); err != nil {
			return fmt.Errorf("restoring hook: %w", err)
		}

		fmt.Printf("\n%s Restored work: %s\n", style.Bold.Render("🪝"), parked.BeadID)
		if parked.Formula != "" {
			fmt.Printf("  Formula: %s\n", parked.Formula)
		}
	}

	// Show context
	if parked.Context != "" {
		fmt.Printf("\n%s Context:\n", style.Bold.Render("📝"))
		fmt.Println(parked.Context)
	}

	// Clear parked work state
	if err := clearParkedWork(cloneRoot, agentID); err != nil {
		// Non-fatal
		fmt.Printf("%s Warning: could not clear parked state: %v\n", style.Dim.Render("⚠"), err)
	}

	fmt.Printf("\n%s Ready to continue!\n", style.Bold.Render("✓"))
	return nil
}

func outputResumeStatus(status ResumeStatus) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(status)
}

func displayResumeStatus(status ResumeStatus, parked *ParkedWork) error {
	if !status.HasParkedWork {
		fmt.Printf("%s No parked work\n", style.Dim.Render("○"))
		return nil
	}

	gateStatus := "open"
	gateIcon := "⏳"
	if status.GateClosed {
		gateStatus = "closed"
		gateIcon = "✓"
	}

	fmt.Printf("%s Parked work status:\n", style.Bold.Render("🅿️"))
	fmt.Printf("  Gate: %s %s (%s)\n", gateIcon, parked.GateID, gateStatus)
	if parked.BeadID != "" {
		fmt.Printf("  Bead: %s\n", parked.BeadID)
	}
	if parked.Formula != "" {
		fmt.Printf("  Formula: %s\n", parked.Formula)
	}
	fmt.Printf("  Parked: %s\n", parked.ParkedAt.Format("2006-01-02 15:04:05"))

	if status.GateClosed {
		fmt.Printf("\n%s Gate cleared! Run 'gt resume' (without --status) to restore work.\n",
			style.Bold.Render("→"))
	}

	return nil
}
