package common

import (
	"os"
	"path"
)

var StartAllDefaultSimple = "client"

func GetCacheFolder() (string, error) {
	cacheFolder := ".cache/dreamland"

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return path.Join(home, cacheFolder), nil
}
