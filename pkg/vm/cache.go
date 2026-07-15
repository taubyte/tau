package vm

import (
	"os"

	"github.com/samyfodil/wazy"
)

var Cache wazy.CompilationCache

func init() {
	cacheDir, err := os.MkdirTemp("", "wazero")
	if err != nil {
		panic(err)
	}

	Cache, err = wazy.NewCompilationCacheWithDir(cacheDir)
	if err != nil {
		panic(err)
	}
}
