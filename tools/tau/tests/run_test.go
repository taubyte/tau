//go:build !cover

package tests

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/taubyte/tau/tools/tau/constants"
)

func takeCover() func() {
	return func() {}
}

func findGoModDir() (string, error) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("could not get the caller information")
	}

	// Start from the directory of the current file
	dir := filepath.Dir(filename)

	// Walk up the directory structure until we find go.mod
	for {
		modPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(modPath); err == nil {
			// go.mod file found
			return dir, nil
		}

		// Move one level up
		parentDir := filepath.Dir(dir)
		if parentDir == dir {
			// We've reached the root without finding go.mod
			break
		}
		dir = parentDir
	}

	return "", fmt.Errorf("go.mod file not found")
}

func (r *roadRunner) Run(args ...string) (string, string, int, error) {
	pd, err := findGoModDir()
	if err != nil {
		return "", "", 1, fmt.Errorf("getting project dir failed with: %s", err)
	}

	_cmd := exec.Command(path.Join(pd, "tools", "tau", "tests", "tau"), args...)
	_cmd.Dir, _ = filepath.Abs(r.dir)
	r.env[constants.TauConfigFileNameEnvVarName] = r.configFile
	r.env[constants.TauSessionLocationEnvVarName] = r.sessionFile
	if r.authUrl != "" {
		// All the tests run with localAuthClient tag, but if the mock tag is false
		// the url is still set to the auth.taubyte.com url
		r.env[constants.AuthURLEnvVarName] = r.authUrl
	}

	_cmd.Env = os.Environ()

	for k, v := range r.env {
		_cmd.Env = append(_cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Capture command output
	var out bytes.Buffer
	var errOut bytes.Buffer
	_cmd.Stdout = &out
	_cmd.Stderr = &errOut

	// Start the command
	err = _cmd.Start()
	if err != nil {
		return "", "", 1, fmt.Errorf("Command failed to start %s", err.Error())
	}

	// Kill the command after the timeout
	done := make(chan bool)
	go func() {
		select {
		case <-time.After(r.waitTime):
			err = _cmd.Process.Kill()
			if err != nil {
				panic(err)
			}
		case <-done:
			return
		}
	}()

	// Wait for the command to finish
	err = _cmd.Wait()
	if err != nil {
		exiterr := err.(*exec.ExitError)
		status := exiterr.Sys().(syscall.WaitStatus)
		return out.String(), errOut.String(), status.ExitStatus(), err
	}
	return out.String(), errOut.String(), 0, nil
}
