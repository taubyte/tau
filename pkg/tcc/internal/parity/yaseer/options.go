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
