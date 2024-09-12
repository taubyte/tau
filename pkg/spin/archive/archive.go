package archive

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"io"
	"sync"

	"go4.org/readerutil"
)

type Archive interface {
	Module(name string) ([]byte, error)
	List() []string
}

type archive struct {
	zip     *zip.Reader
	lock    sync.Mutex
	sources map[string][]byte
}

func New(data []byte) (Archive, error) {
	zipReader, err := zip.NewReader(
		readerutil.NewBufferingReaderAt(bytes.NewBuffer(data)),
		int64(len(data)),
	)
	if err != nil {
		return nil, err
	}
	return &archive{
		zip:     zipReader,
		sources: make(map[string][]byte),
	}, nil
}

func MustNew(data []byte) Archive {
	m, err := New(data)
	if err != nil {
		panic(err)
	}
	return m
}

func (m *archive) Module(name string) ([]byte, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	if src, ok := m.sources[name]; ok {
		return src, nil
	}

	f, err := m.zip.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	src, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	m.sources[name] = src

	return src, nil
}

func (m *archive) List() []string {
	var ret []string
	for _, fi := range m.zip.File {
		ret = append(ret, fi.Name)
	}
	return ret
}
