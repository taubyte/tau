package seer

import (
	"fmt"

	"github.com/spf13/afero"
)

type Option func(s *Seer) error

func SystemFS(path string) Option {
	return func(s *Seer) error {
		if s.fs != nil {
			return fmt.Errorf("can't combile *Fs() Options")
		}
		fs := afero.NewBasePathFs(afero.OsFs{}, path)
		_, err := fs.Stat("/")
		if err != nil {
			return fmt.Errorf("opening repository failed with %w", err)
		}
		s.fs = fs
		return nil
	}
}

func VirtualFS(fs afero.Fs, path string) Option {
	return func(s *Seer) error {
		if s.fs != nil {
			return fmt.Errorf("can't combine *Fs() Options")
		}
		fs = afero.NewBasePathFs(fs, path)
		_, err := fs.Stat("/")
		if err != nil {
			return fmt.Errorf("opening repository failed with %w", err)
		}
		s.fs = fs
		return nil
	}
}

// WithWAL enables write-ahead logging. `path` is the WAL file
// location relative to the FS root (e.g. ".yaseer-wal"). Sync()
// will stage every dirty document into this file before touching
// any real data file; if the process dies mid-Sync, the next
// New() with the same option replays the WAL so the committed
// write is durable.
//
// A leading-slash path stages the WAL above the FS root, which
// the base FS will reject — keep paths relative.
func WithWAL(path string) Option {
	return func(s *Seer) error {
		if path == "" {
			return fmt.Errorf("WithWAL: path must not be empty")
		}
		s.walPath = path
		return nil
	}
}

// WithInMemWAL records every commit's ops in memory as a replayable op-log — no
// file, no byte encoding. The log is exposed via WAL() and replayed into another
// Seer with ReplayWal, e.g. to merge a forked, in-memory edit session into its
// parent without copying files. The parent needs no WAL of its own.
func WithInMemWAL() Option {
	return func(s *Seer) error {
		s.memwal = &memWAL{}
		return nil
	}
}
