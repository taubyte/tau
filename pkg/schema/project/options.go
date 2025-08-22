package project

import (
	"github.com/spf13/afero"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

type Option func(s *project) error

func SystemFS(path string) Option {
	return func(p *project) error {
		var err error
		p.seer, err = seer.New(seer.SystemFS(path))
		return err
	}
}

func VirtualFS(fs afero.Fs, path string) Option {
	return func(p *project) error {
		var err error
		p.seer, err = seer.New(seer.VirtualFS(fs, path))
		return err
	}
}
