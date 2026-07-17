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

	// If a WAL was enabled and a complete record is sitting on disk,
	// the previous process died between fsync(WAL) and Sync()'s
	// data-file writes. Replay before handing the Seer to the caller
	// so the recovered state is what subsequent reads see.
	if err := s.replayWAL(); err != nil {
		return nil, fmt.Errorf("wal replay: %w", err)
	}

	return s, nil
}
