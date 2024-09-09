//go:build !no_rebuild

package tests

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"sync"
	"time"

	"github.com/pterm/pterm"
)

var (
	tauBuilt  bool
	buildLock sync.Mutex

	// Special case override for sending empty values,  will be useful
	// when testing for scripting as currently survey empty values panic with EOF
	// TODO We need to handle EOF for non-required prompts and return
	// Note this should only be used for debugging as other tests could get stuck looping
	promptingEnabled bool

	buildTags = "localAuthClient,projectCreateable,localPatrick,mockGithub"
)

func internalBuildTau() error {
	buildLock.Lock()
	defer buildLock.Unlock()
	if tauBuilt {
		return nil
	}

	tauBuilt = true

	toBuildTags := buildTags
	if !promptingEnabled {
		toBuildTags += ",noPrompt"
	}

	pd, err := findGoModDir()
	if err != nil {
		return fmt.Errorf("getting project dir failed with: %s", err)
	}

	buildStartTime := time.Now()
	buildCmd := exec.Command("go", "build", "--tags", toBuildTags, path.Join(pd, "tools", "tau"))

	var out bytes.Buffer
	var errOut bytes.Buffer
	buildCmd.Stdout = &out
	buildCmd.Stderr = &errOut

	err = buildCmd.Start()
	if err != nil {
		return fmt.Errorf("starting build command failed with: %s", err)
	}

	err = buildCmd.Wait()
	if err != nil {
		fmt.Printf("tau failed to build:\n%s\n", &errOut)
		os.Exit(1)
	}
	// Display buildStartTime
	pterm.Info.Printf("tau built in %s\n", time.Since(buildStartTime))

	_, err = os.Stat("./tau")
	if err != nil {
		return fmt.Errorf("building tau for tests failed with: %s", err)
	}

	return nil
}

// Always rebuild between `go test ...` command executions
func buildTau() error {
	return internalBuildTau()
}
