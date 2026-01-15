package seer

import (
	"fmt"
	"testing"

	"github.com/spf13/afero"
)

func fixtureFS(virtual bool, dir ...string) Option {
	path := ""
	if len(dir) != 0 {
		path = dir[0]
	}

	var newFs afero.Fs
	if virtual {
		newFs = afero.NewBasePathFs(afero.NewMemMapFs(), path)
	} else {
		newFs = afero.NewBasePathFs(afero.OsFs{}, path)
	}

	return func(s *Seer) error {
		_, err := newFs.Stat("/")
		if err != nil {
			return fmt.Errorf("Opening repository failed with %w", err)
		}

		s.fs = newFs
		return nil
	}
}

// newTestSeer creates a Seer instance with VirtualFS using memfs for testing
func newTestSeer(t *testing.T) *Seer {
	t.Helper()
	seer, err := New(VirtualFS(afero.NewMemMapFs(), "/"))
	if err != nil {
		t.Fatalf("Failed to create test seer: %v", err)
	}
	return seer
}
