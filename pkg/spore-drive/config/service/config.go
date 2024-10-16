package service

import (
	"errors"
	"fmt"
	"os"
	"path"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/spore-drive/config"
	"github.com/taubyte/utils/id"
)

func (s *Service) Lookup(id string) (config.Parser, error) {
	cnf := s.getConfig(id)
	if cnf == nil {
		return nil, errors.New("not found")
	}
	return cnf.parser, nil
}

func (s *Service) newConfig(fs afero.Fs, dir string) (*configInstance, error) {
	parser, err := config.New(fs, "/")
	if err != nil {
		return nil, err
	}

	c := &configInstance{
		id:     id.Generate(fmt.Sprintf("%p", parser)),
		fs:     fs,
		parser: parser,
	}

	// only set path for local folders (used by commit)
	if dir != "" && path.IsAbs(dir) {
		st, err := os.Stat(dir)
		if err == nil && st.IsDir() {
			c.path = dir
		}
	}

	s.lock.Lock()
	s.configs[c.id] = c
	s.lock.Unlock()

	return c, nil
}

func (s *Service) freeConfig(id string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	delete(s.configs, id)
}

func (s *Service) getConfig(id string) *configInstance {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.configs[id]
}
