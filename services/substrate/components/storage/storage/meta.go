package storage

import (
	"fmt"
	"io"

	"github.com/ipfs/go-cid"
)

func (m *Meta) Get() (io.ReadSeekCloser, error) {
	file, err := m.node.GetFileFromCid(m.node.Context(), m.cid)
	if err != nil {
		return nil, fmt.Errorf("failed getting file %s with %w", m.cid, err)
	}

	return file, nil
}

func (m *Meta) Version() int {
	return m.version
}

func (m *Meta) Cid() cid.Cid {
	return m.cid
}
