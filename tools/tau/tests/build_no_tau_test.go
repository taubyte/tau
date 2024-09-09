//go:build no_rebuild

package tests

import (
	"os"
)

// Only build if not found
func buildTau() error {
	_, err := os.Stat("./tau-cli")
	if err != nil {
		return internalBuildTau()
	}

	return nil
}
