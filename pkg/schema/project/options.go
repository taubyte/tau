package project

import (
	"github.com/spf13/afero"
	"github.com/taubyte/go-seer"
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
