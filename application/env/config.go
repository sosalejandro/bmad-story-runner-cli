// Package env holds the v6 use cases for per-story test infrastructure:
// port-pool allocation, docker-up coordination, sweeper. Pure orchestration;
// docker-compose invocation happens at the orchestrator-agent layer.
package env

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// PortRange is the per-repo port allocation window from .bmad-test-env.yml.
type PortRange struct {
	Start int `yaml:"start"`
	End   int `yaml:"end"`
}

// Service is one entry under the .bmad-test-env.yml services list.
type Service struct {
	Name           string                 `yaml:"name"`
	Image          string                 `yaml:"image"`
	ResourceLimits map[string]any         `yaml:"resource_limits,omitempty"`
	Env            map[string]string      `yaml:"env,omitempty"`
	Healthcheck    map[string]any         `yaml:"healthcheck,omitempty"`
}

// TestEnvConfig mirrors the §3 / §7 .bmad-test-env.yml schema. Hand-edited per
// repo; loaded by env-up / sweeper.
type TestEnvConfig struct {
	PortRange     PortRange      `yaml:"port_range"`
	PortsPerStory int            `yaml:"ports_per_story"`
	Services      []Service      `yaml:"services"`
	ContainerLabel string        `yaml:"container_label"`
	Mobile        map[string]any `yaml:"mobile,omitempty"`
}

// DefaultTestEnvConfig is the fallback when no .bmad-test-env.yml exists.
// Mirrors the §3 example schema.
func DefaultTestEnvConfig() TestEnvConfig {
	return TestEnvConfig{
		PortRange:      PortRange{Start: 7600, End: 7799},
		PortsPerStory:  10,
		ContainerLabel: "[bmad-test-env]",
	}
}

// LoadTestEnvConfig reads .bmad-test-env.yml from the given dir. Returns the
// default config (and no error) when the file is absent — this is a normal
// state for repos that haven't customized infra yet.
func LoadTestEnvConfig(dir string) (TestEnvConfig, error) {
	path := filepath.Join(dir, ".bmad-test-env.yml")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return DefaultTestEnvConfig(), nil
	}
	if err != nil {
		return TestEnvConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg TestEnvConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return TestEnvConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.PortRange.Start == 0 || cfg.PortRange.End == 0 {
		cfg.PortRange = DefaultTestEnvConfig().PortRange
	}
	if cfg.PortsPerStory == 0 {
		cfg.PortsPerStory = DefaultTestEnvConfig().PortsPerStory
	}
	if cfg.ContainerLabel == "" {
		cfg.ContainerLabel = DefaultTestEnvConfig().ContainerLabel
	}
	return cfg, nil
}
