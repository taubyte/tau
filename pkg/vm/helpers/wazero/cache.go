package helpers

import (
	"os"

	"github.com/tetratelabs/wazero"
)

var cache wazero.CompilationCache

func init() {
	cacheDir, err := os.MkdirTemp("", "wazero")
	if err != nil {
		panic(err)
	}

	cache, err = wazero.NewCompilationCacheWithDir(cacheDir)
	if err != nil {
		panic(err)
	}
}
