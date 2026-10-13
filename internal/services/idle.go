package services

import (
	"fmt"

	"github.com/aliasproject/ingot/internal/config"
	"github.com/aliasproject/ingot/internal/docker"
)

// IdleContainer is the ingot_idle service, which stops idle projects and
// wakes them on the next request (cmd/ingot-idle, internal/idle).
//
// It's run with `docker run` rather than in the compose group: it's
// optional, and compose aborts the whole `up` when one image can't be
// pulled, so an unpublished or unreachable ingot-idle image would otherwise
// take every global service down with it.
const IdleContainer = "ingot_idle"

// idleTimeoutLabel records the INGOT_IDLE_TIMEOUT the running service was
// started with, so status output can report the service's real setting
// rather than whatever the current shell happens to have.
const idleTimeoutLabel = "ingot.idle.timeout"

// startIdle makes sure ingot_idle is running the image this CLI expects.
//
// The timeout is only changed when INGOT_IDLE_TIMEOUT is set in this
// shell. Global services also start implicitly (`ingot start` calls
// EnsureRunning), and a terminal without the variable mustn't silently
// reset a timeout chosen in another.
func (m *Manager) startIdle() error {
	timeout := m.config.IdleTimeout
	info, err := m.docker.InspectContainer(IdleContainer)
	exists := err == nil
	if exists && timeout == "" {
		timeout = info.Labels[idleTimeoutLabel]
	}

	if exists && info.Image == config.IdleImage && info.Labels[idleTimeoutLabel] == timeout {
		if info.State == "running" {
			return nil
		}
		return m.docker.StartContainer(IdleContainer)
	}
	// Get the new image before touching the old container: after a CLI
	// update its tag is new, and if it can't be pulled (offline, not yet
	// published) the old service must keep running rather than leave no
	// waker at all.
	if !m.docker.ImageExists(config.IdleImage) {
		if err := m.docker.PullImage(config.IdleImage, nil); err != nil {
			if exists {
				if info.State != "running" {
					if startErr := m.docker.StartContainer(IdleContainer); startErr != nil {
						return fmt.Errorf("couldn't pull %s (%v), and the previous version wouldn't start either: %w", config.IdleImage, err, startErr)
					}
				}
				return fmt.Errorf("couldn't pull %s, so the previous version keeps running: %w", config.IdleImage, err)
			}
			return fmt.Errorf("couldn't pull %s: %w", config.IdleImage, err)
		}
	}

	if exists {
		if err := m.docker.RemoveContainer(IdleContainer, true); err != nil {
			return fmt.Errorf("failed to replace %s: %w", IdleContainer, err)
		}
	}

	return m.docker.RunContainer(&docker.ContainerConfig{
		Name:     IdleContainer,
		Image:    config.IdleImage,
		Detached: true,
		Restart:  "unless-stopped",
		Network:  config.GetNetworkConfig().BridgeName,
		// Docker Desktop maps this path to its VM's socket on macOS and
		// Windows too. Rootless Docker isn't supported yet.
		Volumes:     []string{"/var/run/docker.sock:/var/run/docker.sock"},
		Environment: map[string]string{"INGOT_IDLE_TIMEOUT": timeout},
		Labels:      map[string]string{idleTimeoutLabel: timeout},
	})
}

// StopIdle stops ingot_idle if it's running and returns its name, so a pause
// can stop it before the projects (otherwise a request arriving mid-pause
// wakes a project that was just stopped) and still resume it afterwards.
func (m *Manager) StopIdle() []string {
	if !m.docker.IsContainerRunning(IdleContainer) {
		return nil
	}
	if err := m.docker.StopContainer(IdleContainer, 10); err != nil {
		return nil
	}
	return []string{IdleContainer}
}

// IdleTimeoutSetting returns the INGOT_IDLE_TIMEOUT ingot_idle was started
// with ("" for its default), and whether the service is running.
func (m *Manager) IdleTimeoutSetting() (string, bool) {
	info, err := m.docker.InspectContainer(IdleContainer)
	if err != nil {
		return "", false
	}
	return info.Labels[idleTimeoutLabel], info.State == "running"
}
