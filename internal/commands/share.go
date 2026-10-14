package commands

import (
	"flag"
	"fmt"
	"os"

	"github.com/aliasproject/ingot/internal/share"
	"github.com/aliasproject/ingot/internal/utils"
)

// handleShare exposes the current project at a public URL through a
// Cloudflare Tunnel:
//
//	ingot share [--auth user:pass]
//	ingot share stop
//	ingot share list
func (h *Handler) handleShare(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "list", "ls":
			return h.handleShareList()
		case "stop":
			return h.handleShareStop()
		}
	}

	fs := flag.NewFlagSet("share", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	authFlag := fs.String("auth", "", "Protect the share with HTTP basic auth (user:password)")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("share: %w", err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unknown share subcommand %q. Use: share [--auth user:pass] | share stop | share list", fs.Arg(0))
	}

	var creds *share.Credentials
	if *authFlag != "" {
		var err error
		if creds, err = share.ParseCredentials(*authFlag); err != nil {
			return err
		}
	}

	project, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}
	if project.Status == "not initialized" {
		return fmt.Errorf("project not initialized. Run 'ingot start' first")
	}
	if err := h.wakeForCommand(); err != nil {
		return err
	}

	mode := "quick tunnel"
	if h.config.ShareNamedMode() {
		mode = "named tunnel (" + h.config.ShareDomain + ")"
	}
	fmt.Printf(utils.Colorize("[STARTING]", utils.ColorCyan)+" Sharing '%s' via Cloudflare %s...\n", project.Name, mode)

	sh, started, err := h.share.Start(project.Name, creds)
	if err != nil {
		return fmt.Errorf("failed to share project: %w", err)
	}

	if !started {
		fmt.Println(utils.Colorize("[INFO]", utils.ColorBlue) + " Already shared")
		if creds != nil {
			fmt.Println("  --auth ignored for an existing share. Run 'ingot share stop' first to change it")
		}
	} else {
		fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Project shared")
	}

	fmt.Printf("\n  %s\n\n", utils.Colorize(sh.URL, utils.ColorGreen))

	if sh.Auth {
		fmt.Println(utils.Colorize("[WARNING]", utils.ColorYellow) + " This URL is reachable from the internet (protected by basic auth)")
	} else {
		fmt.Println(utils.Colorize("[WARNING]", utils.ColorYellow) + " Anyone with this URL can reach your dev site, including debug pages")
		fmt.Println("  Use 'ingot share --auth user:pass' to require a login")
	}
	if sh.Mode == share.ModeQuick {
		fmt.Println("  Quick tunnel URLs change each time you share")
	}
	fmt.Println("  Run 'ingot share stop' to stop sharing")

	return nil
}

func (h *Handler) handleShareStop() error {
	project, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	stopped, err := h.share.Stop(project.Name)
	if err != nil {
		return fmt.Errorf("failed to stop sharing: %w", err)
	}
	if !stopped {
		fmt.Printf(utils.Colorize("[INFO]", utils.ColorBlue)+" '%s' is not shared\n", project.Name)
		return nil
	}

	fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" Stopped sharing '%s'\n", project.Name)
	return nil
}

func (h *Handler) handleShareList() error {
	shares, err := h.share.List()
	if err != nil {
		return fmt.Errorf("failed to list shares: %w", err)
	}

	if len(shares) == 0 {
		fmt.Println("No projects are shared. Run 'ingot share' in a project to share it")
		return nil
	}

	fmt.Printf("%d shared project(s):\n\n", len(shares))
	for _, sh := range shares {
		fmt.Printf("  %s: %s\n", sh.Project, formatShare(sh, h.share.TunnelRunning(sh)))
	}
	return nil
}

// stopShareQuietly ends a project's share as part of stopping or removing the
// project, so a share never outlives the site it points at.
func (h *Handler) stopShareQuietly(projectName string) {
	if projectName == "" {
		return
	}
	stopped, err := h.share.Stop(projectName)
	if err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to stop sharing: %v\n", err)
		return
	}
	if stopped {
		fmt.Println(utils.Colorize("[INFO]", utils.ColorBlue) + " Stopped sharing")
	}
}

// stopAllSharesQuietly ends every share, for commands that take the whole
// stack down.
func (h *Handler) stopAllSharesQuietly() {
	stopped, err := h.share.StopAll()
	if err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to stop shares: %v\n", err)
	}
	if len(stopped) > 0 {
		fmt.Printf(utils.Colorize("[INFO]", utils.ColorBlue)+" Stopped %d share(s)\n", len(stopped))
	}
}

// currentProjectName returns the current directory's project name, or "" if
// it can't be determined.
func (h *Handler) currentProjectName() string {
	if proj, err := h.projects.GetCurrentProject(); err == nil {
		return proj.Name
	}
	return ""
}

func formatShare(sh *share.Share, tunnelRunning bool) string {
	line := sh.URL + " (" + sh.Mode
	if sh.Auth {
		line += ", basic auth"
	}
	line += ")"
	if !tunnelRunning {
		line += " " + utils.Colorize("[STALE]", utils.ColorRed) + " tunnel not running, run 'ingot share stop'"
	}
	return line
}
