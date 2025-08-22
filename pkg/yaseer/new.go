package seer

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

func (s *Seer) Dump() {
	fmt.Printf("Seer documents %+v", s.documents)
}

func New(options ...Option) (*Seer, error) {

	s := &Seer{
		documents: make(map[string]*yaml.Node),
	}

	for _, opt := range options {
		err := opt(s)
		if err != nil {
			return nil, err
		}
	}

	if s.fs == nil {
		return nil, errors.New("can't create a Seer instance without a file system")
	}

	return s, nil
}
