package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/aliasproject/ingot/internal/idle"
	"github.com/aliasproject/ingot/internal/project"
	"github.com/aliasproject/ingot/internal/services"
	"github.com/aliasproject/ingot/internal/utils"
)

// handleIdle shows or changes the current project's scale-to-zero setting:
//
//	ingot idle            show it
//	ingot idle off        never stop this project (e.g. it runs a queue worker)
//	ingot idle on         use the global INGOT_IDLE_TIMEOUT again
//	ingot idle 2h         stop it after its own idle timeout
func (h *Handler) handleIdle(args []string) error {
	proj, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}
	if proj.Status == "not initialized" {
		return fmt.Errorf("project not initialized. Run 'ingot start' first")
	}

	if len(args) == 0 {
		fmt.Printf("%s: %s\n", proj.Name, h.describeIdle(proj.Idle))
		if !h.docker.IsContainerRunning(services.IdleContainer) {
			fmt.Println(utils.Colorize("[WARNING]", utils.ColorYellow) + " ingot_idle isn't running, so nothing is stopped or woken. Run 'ingot global start'.")
		}
		return nil
	}
	if len(args) > 1 {
		return fmt.Errorf("usage: ingot idle [on|off|<duration>]")
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

// wakeForCommand starts the current project if it's asleep, for commands
// that need its container running: exec, shell, share. A project stopped
// with `ingot stop` stays stopped.
func (h *Handler) wakeForCommand() error {
	proj, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}
	// Only a stop by hand is respected. If the vhosts can't be read, wake
	// it: the command was asked for explicitly.
	if proj.Status == "stopped" {
		if stopped, err := h.ssl.StoppedProjects(); err == nil && stopped[proj.Name] {
			return fmt.Errorf("project is stopped. Run 'ingot start' first")
		}
	}
	woke, err := h.projects.Wake()
	if woke {
		fmt.Println(utils.Colorize("[STARTING]", utils.ColorCyan) + " Project was asleep; started it")
	}
	return err
}

// describeIdle explains a project setting against the timeout the running
// ingot_idle service actually uses, not this shell's INGOT_IDLE_TIMEOUT.
func (h *Handler) describeIdle(setting string) string {
	global, _ := h.services.IdleTimeoutSetting()
	return project.DescribeIdle(setting, global)
}

// markStopped records an `ingot stop` (or clears it) in the project's vhost.
// The base Caddyfile is rewritten first so the snippet the vhost will import
// exists even if services were started by an older ingot.
func (h *Handler) markStopped(name string, stopped bool) error {
	if err := h.services.WriteBaseConfig(); err != nil {
		return err
	}
	return h.ssl.SetProjectStopped(name, stopped)
}

// clearStopped makes a project wake on visits again, for commands that start
// it other than `ingot start` (rebuild, php, image).
func (h *Handler) clearStopped(name string) {
	if err := h.markStopped(name, false); err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to re-enable waking for the project: %v\n", err)
	}
}

// projectAsleep reports whether a stopped project starts again on the next
// visit: ingot_idle is running and the project wasn't stopped with `ingot
// stop`.
func (h *Handler) projectAsleep(name, status string) bool {
	if status != "stopped" || !h.docker.IsContainerRunning(services.IdleContainer) {
		return false
	}
	stopped, err := h.ssl.StoppedProjects()
	return err == nil && !stopped[name]
}

// formatProjectStatus shows a stopped project as sleeping when the next
// request to it starts it again.
func (h *Handler) formatProjectStatus(status string, asleep bool) string {
	if status == "stopped" && asleep {
		return utils.Colorize("[SLEEPING]", utils.ColorBlue) + " sleeping, starts on the next visit"
	}
	return utils.FormatStatus(status)
}

// hintVhostsWithoutWake points out project vhosts written before scale to
// zero, which don't start a stopped project on a visit. Each is rewritten on
// that project's next `ingot start`; `ingot cert repair` does them all.
func (h *Handler) hintVhostsWithoutWake() {
	output, err := h.docker.RunWithOutput("run", "--rm", "-v", "ingot_vhosts:/vhosts:ro", "alpine:latest",
		"sh", "-c", "cd /vhosts && grep -LE 'import ingot_(wake|stopped)' *.caddyfile 2>/dev/null; true")
	if err != nil {
		return
	}
	// Output can include docker's own pull messages, so count only file
	// names. Share vhosts are skipped: cert repair doesn't rewrite them (the
	// next `ingot share` does), so counting them would repeat the hint.
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
	fmt.Printf(utils.Colorize("[INFO]", utils.ColorBlue)+" %d vhost(s) predate scale to zero, so those projects won't start on a visit. Run 'ingot cert repair' to update them.\n", n)
}

// doctorIdle checks the scale-to-zero service and reports projects it has
// flagged as busy for a long time with no traffic.
func (h *Handler) doctorIdle(r *doctorReport) {
	r.section("SCALE TO ZERO")

	if _, err := idle.ParseTimeout(h.config.IdleTimeout, idle.DefaultTimeout); err != nil {
		r.fail(fmt.Sprintf("INGOT_IDLE_TIMEOUT=%q isn't a duration", h.config.IdleTimeout),
			"set it to e.g. 30m or 2h, or off, then run 'ingot global restart'")
	}

	running, ok := h.services.IdleTimeoutSetting()
	if !ok {
		r.fail("ingot_idle isn't running: idle projects aren't stopped, and stopped ones aren't started on a visit",
			"ingot global start (its output says why it couldn't start, e.g. the image couldn't be pulled)")
		return
	}
	if h.config.IdleTimeout != "" && h.config.IdleTimeout != running {
		r.warn(fmt.Sprintf("INGOT_IDLE_TIMEOUT is %q here but ingot_idle runs with %q", h.config.IdleTimeout, running),
			"ingot global restart, to apply this shell's value")
	}
	r.ok("ingot_idle running (%s)", strings.TrimPrefix(h.describeIdle(""), "stops "))

	logs, err := h.docker.RunWithOutput("logs", "--since", "24h", services.IdleContainer)
	if err != nil {
		r.warn(fmt.Sprintf("Could not read ingot_idle logs: %v", err), "")
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
