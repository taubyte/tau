package embed

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"io"
	"sync"

	"go4.org/readerutil"
)

var (
	//go:embed assets/runtimes.zip
	runtimesData    []byte
	runtimesArchive = mustModulesArchive(runtimesData)

	//go:embed assets/tools.zip
	toolsData    []byte
	toolsArchive = mustModulesArchive(toolsData)
)

type modulesArchive struct {
	zip     *zip.Reader
	lock    sync.Mutex
	sources map[string][]byte
}

func newModulesArchive(data []byte) (*modulesArchive, error) {
	zipReader, err := zip.NewReader(
		readerutil.NewBufferingReaderAt(bytes.NewBuffer(data)),
		int64(len(data)),
	)
	if err != nil {
		return nil, err
	}
	return &modulesArchive{
		zip:     zipReader,
		sources: make(map[string][]byte),
	}, nil
}

func mustModulesArchive(data []byte) *modulesArchive {
	m, err := newModulesArchive(data)
	if err != nil {
		panic(err)
	}
	return m
}

func (m *modulesArchive) Module(name string) ([]byte, error) {
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

func RuntimeADM64() ([]byte, error) {
	return runtimesArchive.Module("amd64.wasm")
}

func RuntimeRISCV64() ([]byte, error) {
	return runtimesArchive.Module("riscv64.wasm")
}

func ToolsSquashFS() ([]byte, error) {
	return toolsArchive.Module("squashfs.wasm")
}
