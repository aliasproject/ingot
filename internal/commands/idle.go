package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/aliasproject/alios-cli/internal/idle"
	"github.com/aliasproject/alios-cli/internal/project"
	"github.com/aliasproject/alios-cli/internal/services"
	"github.com/aliasproject/alios-cli/internal/utils"
)

// handleIdle shows or changes the current project's scale-to-zero setting:
//
//	alios idle            show it
//	alios idle off        never stop this project (e.g. it runs a queue worker)
//	alios idle on         use the global ALIOS_IDLE_TIMEOUT again
//	alios idle 2h         stop it after its own idle timeout
func (h *Handler) handleIdle(args []string) error {
	proj, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}
	if proj.Status == "not initialized" {
		return fmt.Errorf("project not initialized. Run 'alios start' first")
	}

	if len(args) == 0 {
		fmt.Printf("%s: %s\n", proj.Name, h.describeIdle(proj.Idle))
		if !h.docker.IsContainerRunning(services.IdleContainer) {
			fmt.Println(utils.Colorize("[WARNING]", utils.ColorYellow) + " alios_idle isn't running, so nothing is stopped or woken. Run 'alios global start'.")
		}
		return nil
	}
	if len(args) > 1 {
		return fmt.Errorf("usage: alios idle [on|off|<duration>]")
	}

	setting := strings.TrimSpace(args[0])
	if setting == "on" || setting == "default" {
		setting = ""
	}
	if setting == "never" {
		setting = "off"
	}

	// Changing the label recreates the container, which counts as a fresh
	// start for the idle timer, so this never stops the project right away.
	if err := h.projects.SetIdle(setting); err != nil {
		return err
	}
	fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" %s: %s\n", proj.Name, h.describeIdle(setting))
	return nil
}

// wakeForCommand starts the current project if it's asleep (or stopped), for
// commands that need its container running: exec, shell, share.
func (h *Handler) wakeForCommand() error {
	woke, err := h.projects.Wake()
	if woke {
		fmt.Println(utils.Colorize("[STARTING]", utils.ColorCyan) + " Project was stopped; started it")
	}
	return err
}

// describeIdle explains a project setting against the timeout the running
// alios_idle service actually uses, not this shell's ALIOS_IDLE_TIMEOUT.
func (h *Handler) describeIdle(setting string) string {
	global, _ := h.services.IdleTimeoutSetting()
	return project.DescribeIdle(setting, global)
}

// formatProjectStatus shows a stopped project as sleeping when alios_idle is
// running, because the next request to it starts it again.
func (h *Handler) formatProjectStatus(status string, waking bool) string {
	if status == "stopped" && waking {
		return utils.Colorize("[SLEEPING]", utils.ColorBlue) + " sleeping, starts on the next visit"
	}
	return utils.FormatStatus(status)
}

// hintVhostsWithoutWake points out project vhosts written before scale to
// zero, which don't start a stopped project on a visit. Each is rewritten on
// that project's next `alios start`; `alios cert repair` does them all.
func (h *Handler) hintVhostsWithoutWake() {
	output, err := h.docker.RunWithOutput("run", "--rm", "-v", "alios_vhosts:/vhosts:ro", "alpine:latest",
		"sh", "-c", "cd /vhosts && grep -L 'import alios_wake' *.caddyfile 2>/dev/null; true")
	if err != nil {
		return
	}
	// Output can include docker's own pull messages, so count only file
	// names. Share vhosts are skipped: cert repair doesn't rewrite them (the
	// next `alios share` does), so counting them would repeat the hint.
	n := 0
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ".caddyfile") && !strings.HasSuffix(line, ".share.caddyfile") {
			n++
		}
	}
	if n == 0 {
		return
	}
	fmt.Printf(utils.Colorize("[INFO]", utils.ColorBlue)+" %d vhost(s) predate scale to zero, so those projects won't start on a visit. Run 'alios cert repair' to update them.\n", n)
}

// doctorIdle checks the scale-to-zero service and reports projects it has
// flagged as busy for a long time with no traffic.
func (h *Handler) doctorIdle(r *doctorReport) {
	r.section("SCALE TO ZERO")

	if _, err := idle.ParseTimeout(h.config.IdleTimeout, idle.DefaultTimeout); err != nil {
		r.fail(fmt.Sprintf("ALIOS_IDLE_TIMEOUT=%q isn't a duration", h.config.IdleTimeout),
			"set it to e.g. 30m or 2h, or off, then run 'alios global restart'")
	}

	running, ok := h.services.IdleTimeoutSetting()
	if !ok {
		r.fail("alios_idle isn't running: idle projects aren't stopped, and stopped ones aren't started on a visit",
			"alios global start (its output says why it couldn't start, e.g. the image couldn't be pulled)")
		return
	}
	if h.config.IdleTimeout != "" && h.config.IdleTimeout != running {
		r.warn(fmt.Sprintf("ALIOS_IDLE_TIMEOUT is %q here but alios_idle runs with %q", h.config.IdleTimeout, running),
			"alios global restart, to apply this shell's value")
	}
	r.ok("alios_idle running (%s)", strings.TrimPrefix(h.describeIdle(""), "stops "))

	logs, err := h.docker.RunWithOutput("logs", "--since", "24h", services.IdleContainer)
	if err != nil {
		r.warn(fmt.Sprintf("Could not read alios_idle logs: %v", err), "")
		return
	}
	busy := 0
	for _, line := range strings.Split(logs, "\n") {
		if i := strings.Index(line, idle.BusyWarning+" "); i >= 0 {
			busy++
			r.warn(strings.TrimSpace(line[i+len(idle.BusyWarning):]), "")
		}
	}
	if busy == 0 {
		r.ok("No project busy for hours without traffic (last %s)", 24*time.Hour)
	}
}
