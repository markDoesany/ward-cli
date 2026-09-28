package project

import (
	"fmt"

	"github.com/ward/ward/internal/config"
)

type Manager struct {
	Projects map[string]config.ProjectConfig
	Paths    map[string]string
}

func Load(configDir string) (*Manager, error) {
	projects, paths, err := config.Discover(configDir)
	if err != nil {
		return nil, err
	}
	return &Manager{Projects: projects, Paths: paths}, nil
}

func (m *Manager) Get(id string) (config.ProjectConfig, error) {
	cfg, ok := m.Projects[id]
	if !ok {
		return config.ProjectConfig{}, fmt.Errorf("project %q not found", id)
	}
	return cfg, nil
}
