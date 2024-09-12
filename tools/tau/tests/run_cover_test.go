//go:build cover

package tests

import (
	"fmt"
	"io/ioutil"
	"os"
	"sync"

	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/cli"
	"github.com/taubyte/tau/tools/tau/constants"
	"github.com/taubyte/tau/tools/tau/env"
	authClient "github.com/taubyte/tau/tools/tau/singletons/auth_client"
	"github.com/taubyte/tau/tools/tau/singletons/config"
	"github.com/taubyte/tau/tools/tau/singletons/session"
)

var coverLock sync.Mutex

func takeCover() func() {
	buildLock.Lock()
	session.Clear()
	config.Clear()
	authClient.Clear()
	env.Clear()
	return buildLock.Unlock
}

func (r *roadRunner) Run(args ...string) (err error, code int, out string, errOut string) {
	coverLock.Lock()
	defer coverLock.Unlock()
	defer func() {
		// Capture panic and return err, 2, "", "" if panic
		if r := recover(); r != nil {
			err = fmt.Errorf("command failed with panic: %v", r)
			errOut = r.(string)
			code = 2
			return
		}
	}()

	r.env[constants.TauConfigFileNameEnvVarName] = r.configFile
	r.env[constants.TauSessionLocationEnvVarName] = r.sessionFile
	if r.authUrl != "" {
		// All the tests run with localAuthClient tag, but if the mock tag is false
		// the url is still set to the auth.taubyte.com url
		r.env[constants.AuthURLEnvVarName] = r.authUrl
	}

	for key, value := range r.env {
		err = os.Setenv(key, value)
		if err != nil {
			return
		}

		defer os.Unsetenv(key)
	}

	constants.TauConfigFileName = r.configFile

	oldDir, err := os.Getwd()
	if err != nil {
		return
	}

	err = os.Chdir(r.dir)
	if err != nil {
		return
	}
	defer os.Chdir(oldDir)

	rescueStdout := os.Stdout
	rdr, wtr, _ := os.Pipe()
	os.Stdout = wtr
	pterm.SetDefaultOutput(wtr)

	err = cli.Run(append([]string{"prog"}, args...)...)
	if err != nil {
		errOut = err.Error()
		code = 1
	}

	// back to normal state
	wtr.Close()

	outB, _ := ioutil.ReadAll(rdr)
	os.Stdout = rescueStdout
	pterm.SetDefaultOutput(rescueStdout)

	out = string(outB)

	return
}
