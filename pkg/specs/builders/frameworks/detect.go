package frameworks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// PackageJSON is the subset of a package.json we need to recognise a framework.
type PackageJSON struct {
	Name            string            `json:"name"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

// allDeps merges runtime and dev dependencies into a single lookup set.
func (p *PackageJSON) allDeps() map[string]struct{} {
	deps := make(map[string]struct{}, len(p.Dependencies)+len(p.DevDependencies))
	for name := range p.Dependencies {
		deps[name] = struct{}{}
	}
	for name := range p.DevDependencies {
		deps[name] = struct{}{}
	}
	return deps
}

// ParsePackageJSON decodes a package.json document.
func ParsePackageJSON(data []byte) (*PackageJSON, error) {
	p := &PackageJSON{}
	if err := json.Unmarshal(data, p); err != nil {
		return nil, fmt.Errorf("parsing package.json failed with: %w", err)
	}
	return p, nil
}

// Detect resolves the framework of a project from its package.json and the set
// of files present at its root. The highest priority match wins; ties are
// broken by registry order. It returns an error when nothing matches.
func Detect(pkg *PackageJSON, files map[string]bool) (*Framework, error) {
	var deps map[string]struct{}
	if pkg != nil {
		deps = pkg.allDeps()
	} else {
		deps = map[string]struct{}{}
	}

	var best *Framework
	for _, f := range Registry {
		if !matches(f, deps, files) {
			continue
		}
		if best == nil || f.Priority > best.Priority {
			best = f
		}
	}

	if best == nil {
		return nil, fmt.Errorf("no supported framework detected")
	}

	return best, nil
}

func matches(f *Framework, deps map[string]struct{}, files map[string]bool) bool {
	for _, dep := range f.Dependencies {
		if _, ok := deps[dep]; ok {
			return true
		}
	}
	for _, file := range f.ConfigFiles {
		if files[file] {
			return true
		}
	}
	return false
}

// DetectDir inspects a project directory on disk: it reads package.json (when
// present) and lists root files, then resolves the framework.
func DetectDir(dir string) (*Framework, error) {
	files := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading project directory `%s` failed with: %w", dir, err)
	}
	for _, e := range entries {
		files[e.Name()] = true
	}

	var pkg *PackageJSON
	if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		if pkg, err = ParsePackageJSON(data); err != nil {
			return nil, err
		}
	}

	return Detect(pkg, files)
}
