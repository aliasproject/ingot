// Package state persists small pieces of ephemeral runtime state (currently just
// pause/resume snapshots) inside a dedicated Docker volume rather than a host
// config file, matching this codebase's "no local config files" design.
package state

import (
	"encoding/json"
	"time"

	"github.com/aliasproject/alios-cli/internal/docker"
)

const (
	// VolumeName is the Docker volume used to store alios runtime state.
	VolumeName = "alios_state"

	// PauseFile is the file within VolumeName holding the pause snapshot.
	PauseFile = "pause-state.json"
)

// PauseState records which containers were running at the moment `alios pause` ran.
type PauseState struct {
	PausedAt time.Time `json:"paused_at"`
	Services []string  `json:"services"`
	Projects []string  `json:"projects"`
}

// Store reads and writes PauseState via a Docker volume.
type Store struct {
	docker *docker.Client
}

// NewStore creates a new state Store.
func NewStore(dockerClient *docker.Client) *Store {
	return &Store{docker: dockerClient}
}

// SavePauseState records the given project and service container names as the
// current pause snapshot.
func (s *Store) SavePauseState(projects, services []string) error {
	ps := PauseState{
		PausedAt: time.Now(),
		Services: services,
		Projects: projects,
	}

	data, err := json.Marshal(ps)
	if err != nil {
		return err
	}

	return s.docker.WriteFileToVolume(VolumeName, PauseFile, data)
}

// LoadPauseState returns the most recent pause snapshot, or (nil, nil) if none exists.
func (s *Store) LoadPauseState() (*PauseState, error) {
	data, err := s.docker.ReadFileFromVolume(VolumeName, PauseFile)
	if err != nil {
		if err == docker.ErrFileNotFound {
			return nil, nil
		}
		return nil, err
	}

	var ps PauseState
	if err := json.Unmarshal([]byte(data), &ps); err != nil {
		return nil, err
	}

	return &ps, nil
}

// ClearPauseState erases the pause snapshot after a successful resume.
func (s *Store) ClearPauseState() error {
	return s.docker.WriteFileToVolume(VolumeName, PauseFile, []byte("{}"))
}
