package project

import (
	"fmt"
	"time"

	"github.com/aliasproject/alios-cli/internal/idle"
)

// SetIdle changes the current project's scale-to-zero setting and recreates
// the container to store it: "" uses the global ALIOS_IDLE_TIMEOUT, "off"
// keeps the project running, a duration ("2h") is its own idle timeout.
func (m *Manager) SetIdle(setting string) error {
	if setting != "" && setting != "off" {
		d, err := time.ParseDuration(setting)
		if err != nil || d <= 0 {
			return fmt.Errorf("invalid idle setting %q: use on, off, or a duration such as 45m or 2h", setting)
		}
	}

	project, err := m.GetCurrentProject()
	if err != nil {
		return err
	}
	if project.Status == "not initialized" {
		return fmt.Errorf("project not initialized")
	}
	if project.Idle == setting {
		return nil
	}

	project.Idle = setting
	return m.recreate(project)
}

// Wake starts the current project's container if it's stopped - asleep after
// going idle, or stopped by hand - and waits until it's running. Commands
// that run inside the container call it, so they work on a sleeping project.
func (m *Manager) Wake() (woke bool, err error) {
	project, err := m.GetCurrentProject()
	if err != nil {
		return false, err
	}
	if project.Status == "not initialized" {
		return false, fmt.Errorf("project not initialized. Run 'alios start' first")
	}
	if project.Status == "running" {
		return false, nil
	}

	if err := m.docker.StartContainer(project.Container); err != nil {
		return false, fmt.Errorf("failed to start project container: %w", err)
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if m.docker.IsContainerRunning(project.Container) {
			return true, nil
		}
	}
	return false, fmt.Errorf("project container didn't start; check 'alios logs'")
}

// DescribeIdle explains a project's idle setting for status output.
func DescribeIdle(setting, globalTimeout string) string {
	switch setting {
	case "off":
		return "never stopped (alios idle off)"
	case "":
		timeout, err := idle.ParseTimeout(globalTimeout, idle.DefaultTimeout)
		if err != nil {
			return "stops when idle (invalid ALIOS_IDLE_TIMEOUT, using the default)"
		}
		if timeout == 0 {
			return "never stopped (ALIOS_IDLE_TIMEOUT=off)"
		}
		return fmt.Sprintf("stops after %s idle", timeout)
	}
	return fmt.Sprintf("stops after %s idle (project setting)", setting)
}
