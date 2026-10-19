package project

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/afero"

	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// starter is the project `tau init` writes: one function behind one route in
// one zone. Its documents carry no ids — every id is minted by the vocabulary's
// generator when the project is created, so two projects never share one.
//
//go:embed starter
var starter embed.FS

// ErrExists is a directory that already holds a project.
var ErrExists = errors.New("this directory already holds a project (project.yaml); pass --force to overwrite")

// Init writes the starter project into dir under name and returns the paths
// written. The project is compiled before anything touches the disk, so a
// starter that does not compile is never written.
func Init(ctx context.Context, dir, name string, force bool) ([]string, error) {
	mem := afero.NewMemMapFs()
	var files []string
	err := fs.WalkDir(starter, "starter", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := starter.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("starter", path)
		rel = filepath.ToSlash(rel)
		files = append(files, rel)
		return afero.WriteFile(mem, "/"+rel, data, 0o644)
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)

	s, err := schema.AdoptSession(mem)
	if err != nil {
		return nil, err
	}
	p := &Project{dir: dir, s: s, touched: map[string][]string{}}
	if !force {
		if existing, err := Open(dir); err == nil && existing.IsProject() {
			return nil, ErrExists
		}
	}

	idField, nameField := identity()
	for _, rel := range files {
		res, ok := s.ResourceAt(rel)
		if !ok {
			return nil, fmt.Errorf("starter: %s is not a resource document", rel)
		}
		id, err := s.Generate(res, []string{idField}, name)
		if err != nil {
			return nil, err
		}
		if err := s.Set(res, []string{idField}, id); err != nil {
			return nil, err
		}
		p.touch(res)
	}
	if err := s.Set(s.Root(), []string{nameField}, name); err != nil {
		return nil, err
	}

	if r := p.Validate(ctx, ""); r.Error != "" {
		return nil, fmt.Errorf("starter project does not compile as %q: %s", name, r.Error)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return p.Save()
}
