package helpers

import (
	"os"

	"github.com/tetratelabs/wazero"
)

var Cache wazero.CompilationCache

func init() {
	cacheDir, err := os.MkdirTemp("", "wazero")
	if err != nil {
		panic(err)
	}

	Cache, err = wazero.NewCompilationCacheWithDir(cacheDir)
	if err != nil {
		panic(err)
	}
}
