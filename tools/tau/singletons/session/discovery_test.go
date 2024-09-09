package session

import (
	"os"
	"testing"

	"github.com/mitchellh/go-ps"
)

func TestDiscovery(t *testing.T) {
	// Overriding the prefix so that the test does not conflict with actual values
	oldPrefix := sessionDirPrefix
	sessionDirPrefix = "tau-test"
	defer func() {
		sessionDirPrefix = oldPrefix
	}()

	parentProcess, err := ps.FindProcess(os.Getppid())
	if err != nil {
		t.Error(err)
		return
	}

	expectedDir := directoryFromPid(parentProcess.PPid())
	dir, err := discoverOrCreateConfigFileLoc()
	if err != nil {
		t.Error(err)
		return
	}

	if dir != expectedDir {
		t.Errorf("Expected %s, got %s", expectedDir, dir)
		return
	}

	err = os.Remove(expectedDir)
	if err != nil {
		t.Error(err)
		return
	}
}
