package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LinkFile is where a project directory remembers which cloud project it is,
// relative to the directory.
const LinkFile = ".tau/link.yaml"

// Link is a project directory's binding to a cloud. The project id is
// project.yaml's own — the cloud mints none — so this records only which cloud
// profile it was linked on and which repositories the link claimed. It holds
// no credential, so it may be committed or not as the developer likes.
type Link struct {
	ProjectID string   `yaml:"project_id" json:"project_id"`
	Name      string   `yaml:"name" json:"name"`
	Profile   string   `yaml:"profile,omitempty" json:"profile,omitempty"`
	Config    string   `yaml:"config_repository_key" json:"config_repository_key"`
	Code      []string `yaml:"code_repository_keys,omitempty" json:"code_repository_keys,omitempty"`
}

// ErrNotLinked is a directory no `tau link` has run in.
var ErrNotLinked = errors.New("this directory is not linked to a project: run `tau link`, or pass --project")

// ReadLink reads dir's link.
func ReadLink(dir string) (*Link, error) {
	data, err := os.ReadFile(filepath.Join(dir, LinkFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotLinked
	}
	if err != nil {
		return nil, err
	}
	var l Link
	if err := yaml.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("reading %s: %w", LinkFile, err)
	}
	if l.ProjectID == "" {
		return nil, ErrNotLinked
	}
	return &l, nil
}

// WriteLink writes dir's link.
func WriteLink(dir string, l Link) error {
	path := filepath.Join(dir, LinkFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(l)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
