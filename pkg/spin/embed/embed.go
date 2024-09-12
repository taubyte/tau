package embed

import (
	_ "embed"

	"github.com/taubyte/tau/pkg/spin/archive"
)

var (
	//go:embed assets/runtimes.zip
	runtimesData    []byte
	runtimesArchive = archive.MustNew(runtimesData)

	//go:embed assets/tools.zip
	toolsData    []byte
	toolsArchive = archive.MustNew(toolsData)
)

func RuntimeADM64() ([]byte, error) {
	return runtimesArchive.Module("amd64.wasm")
}

func RuntimeRISCV64() ([]byte, error) {
	return runtimesArchive.Module("riscv64.wasm")
}

func ToolsSquashFS() ([]byte, error) {
	return toolsArchive.Module("squashfs.wasm")
}
