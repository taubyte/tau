package drive

import (
	"fmt"
	"os"
	"path"
	"strings"
)

type Option func(Spore) error

func WithTauLatest() Option {
	return func(s Spore) error {
		sd := s.(*sporedrive)
		r, err := sd.releases.latestVersion()
		if err != nil {
			return fmt.Errorf("fetching latest release: %w", err)
		}

		sd.tauBinary, err = sd.releases.tau(r, "amd64")
		if err != nil {
			return fmt.Errorf("downloading latest (v%s) release: %w", r, err)
		}

		return nil
	}
}

func WithTauVersion(version string) Option {
	return func(s Spore) (err error) {
		sd := s.(*sporedrive)
		sd.tauBinary, err = sd.releases.tau(version, "amd64")
		if err != nil {
			return fmt.Errorf("downloading %v release: %w", version, err)
		}

		return nil
	}
}

func WithTauUrl(url string) Option {
	return func(s Spore) (err error) {
		s.(*sporedrive).tauBinary, err = httpDownload(url)
		if err != nil {
			return fmt.Errorf("downloading tau: %w", err)
		}

		return nil
	}
}

func WithTauPath(path string) Option {
	return func(s Spore) (err error) {
		s.(*sporedrive).tauBinary, err = os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading tau from %s: %w", path, err)
		}

		return nil
	}
}

// WithRoot is where tau lives on every host: its binary in bin, the node files
// in config, the nodes' data in storage. Empty keeps DefaultRoot.
func WithRoot(root string) Option {
	return func(s Spore) error {
		if root == "" {
			return nil
		}
		if !path.IsAbs(root) || path.Clean(root) != root || root == "/" {
			return fmt.Errorf("root %q is not a clean absolute directory", root)
		}
		for _, part := range strings.Split(strings.TrimPrefix(root, "/"), "/") {
			if !safeName.MatchString(part) {
				return fmt.Errorf("root %q: %q is letters, digits, '.', '_' and '-'", root, part)
			}
		}
		s.(*sporedrive).root = root
		return nil
	}
}
