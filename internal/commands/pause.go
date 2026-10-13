package commands

import (
	"fmt"

	"github.com/aliasproject/ingot/internal/state"
	"github.com/aliasproject/ingot/internal/utils"
)

// handlePause stops every ingot-managed container on the machine - all project
// containers across all project directories, plus the shared global services
// (dns, caddy, mail, databases) - and remembers exactly what was running so that
// handleResume can bring back only that set.
func (h *Handler) handlePause() error {
	fmt.Println(utils.Colorize("[STOPPING]", utils.ColorYellow) + " Pausing ingot...")

	// Shares aren't restored on resume: a quick tunnel would come back at a
	// different URL anyway, so end them rather than leave stale vhosts.
	stoppedShares, err := h.share.StopAll()
	if err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to stop shares: %v\n", err)
	}

	// The waker goes first: stopping projects takes a while, and a request
	// arriving in the meantime would start one again behind the pause.
	stoppedIdle := h.services.StopIdle()

	stoppedProjects, err := h.projects.StopAll()
	if err != nil {
		return fmt.Errorf("failed to stop project containers: %w", err)
	}

	stoppedServices, err := h.services.Pause()
	if err != nil {
		return fmt.Errorf("failed to stop global services: %w", err)
	}
	stoppedServices = append(stoppedServices, stoppedIdle...)

	if len(stoppedProjects) == 0 && len(stoppedServices) == 0 {
		fmt.Println(utils.Colorize("[INFO]", utils.ColorBlue) + " Nothing was running")
		return nil
	}

	store := state.NewStore(h.docker)
	if err := store.SavePauseState(stoppedProjects, stoppedServices); err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to save pause state: %v\n", err)
		fmt.Println("   'ingot resume' won't know what to bring back. Run 'ingot global start' and start each project manually.")
	}

	fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Ingot paused")
	fmt.Printf("  %d project container(s) stopped\n", len(stoppedProjects))
	fmt.Printf("  %d global service container(s) stopped\n", len(stoppedServices))
	if len(stoppedShares) > 0 {
		fmt.Printf("  %d share(s) ended (run 'ingot share' again after resuming)\n", len(stoppedShares))
	}
	fmt.Println("\nRun 'ingot resume' when you're ready to start working again")

	return nil
}

// handleResume restores exactly the containers that were running at the last
// ingot pause.
func (h *Handler) handleResume() error {
	store := state.NewStore(h.docker)
	ps, err := store.LoadPauseState()
	if err != nil {
		return fmt.Errorf("failed to read pause state: %w", err)
	}

	if ps == nil || (len(ps.Projects) == 0 && len(ps.Services) == 0) {
		fmt.Println(utils.Colorize("[INFO]", utils.ColorBlue) + " No paused ingot session found")
		return nil
	}

	fmt.Println(utils.Colorize("[STARTING]", utils.ColorCyan) + " Resuming ingot...")

	if err := h.services.Resume(ps.Services); err != nil {
		return fmt.Errorf("failed to resume global services: %w", err)
	}

	startedProjects, err := h.projects.StartNamed(ps.Projects)
	if err != nil {
		return fmt.Errorf("failed to resume project containers: %w", err)
	}

	if err := store.ClearPauseState(); err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to clear pause state: %v\n", err)
	}

	fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Ingot resumed")
	fmt.Printf("  %d global service container(s) started\n", len(ps.Services))
	fmt.Printf("  %d project container(s) started\n", len(startedProjects))

	return nil
}
