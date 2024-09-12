package tests

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pterm/pterm"
	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/cli/common"
	"github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/tools/tau/singletons/session"
	"gotest.tools/v3/assert"
)

func newMonkey(s *spiderTestContext, tm testMonkey) *monkeyTestContext {
	return &monkeyTestContext{
		spider:     s,
		testMonkey: tm,
	}
}

func newMonkeyRunContext(tm testMonkey, rr roadRunner, isChild bool) *monkeyRunContext {
	prefix := "Command"
	if isChild {
		prefix = "Child-Command"
	}

	return &monkeyRunContext{
		prefix:     prefix,
		testMonkey: tm,
		rr:         rr,
		isChild:    isChild,
	}
}

func (ctx *monkeyRunContext) checkSession(t *testing.T, result *commandResult) {
	if ctx.evaluateSession != nil {
		sessionLock.Lock()
		defer sessionLock.Unlock()

		err := session.LoadSessionInDir(ctx.rr.sessionFile)
		if err != nil {
			t.Errorf("loading session at `%s` failed with: %s", ctx.rr.sessionFile, err)
			return
		}

		err = ctx.evaluateSession(session.Get())
		if err != nil {
			result.Error(t, fmt.Sprintf("Session evaluation failed with: %s", err.Error()))
			return
		}
	}
}

func (ctx *monkeyRunContext) checkProject(t *testing.T) {
	if ctx.confirmProject != nil {
		_project, err := project.Open(project.VirtualFS(afero.NewOsFs(), path.Join(ctx.rr.dir, "test_project/config")))
		if err != nil {
			panic(err)
		}

		err = ctx.confirmProject(_project)
		if err != nil {
			t.Errorf("\n\nConfirming project failed with error: %s\n\n", err)
			return
		}
	}
}

func (ctx *monkeyRunContext) Run(t *testing.T) {
	result := &commandResult{
		monkeyRunContext: ctx,
	}
	result.out1, result.out2, result.exitCode, result.err = ctx.rr.Run(ctx.args...)
	result.printDebugInfo()

	// confirm error is as expected, either nil or containing string
	result.checkError(t)

	// confirm wantOut and dontWantOut
	result.checkWantOut(t)

	// Check exit code, defaults 0
	result.checkExitCode(t)

	// confirm wantDir and dontWantDir
	result.checkDirectories(t)

	// Run confirmProject
	ctx.checkProject(t)

	// Run evaluateSession
	ctx.checkSession(t, result)
}

func (tm *monkeyTestContext) setParallel(t *testing.T) {
	if tm.spider.parallel {
		t.Parallel()
	}
}

func (tm *monkeyTestContext) getOrCreateDir() (err error) {
	// Create temp configFile
	if tm.spider.debug {
		tm.dir = "./_fakeroot/debug/" + tm.name
	} else {
		tm.dir, err = os.MkdirTemp("_fakeroot", "tests")
		if err != nil {
			return err
		}
	}

	// Transform dir to an absolute
	tm.dir, err = filepath.Abs(tm.dir)
	if err != nil {
		return err
	}

	// Define the location of the config file
	// in the temp directory, or named directory if in
	// debug mode
	tm.configLoc, _ = filepath.Abs(tm.dir + "/tau.yaml")
	tm.sessionLoc, _ = filepath.Abs(tm.dir + "/session")

	return nil
}

func (tm *monkeyTestContext) refreshTestFiles() error {
	// Remove previous debug files, as they don't get removed
	// when debugging
	if tm.spider.debug {
		os.Remove(tm.configLoc)
		os.Remove(tm.sessionLoc)
		os.RemoveAll(tm.dir)
	}

	// Create the project config and code directories for testing
	if tm.spider.projectName != "" {
		err := os.MkdirAll(fmt.Sprintf("%s/%s/config", tm.dir, tm.spider.projectName), 0755)
		if err != nil {
			return err
		}

		err = os.MkdirAll(fmt.Sprintf("%s/%s/code", tm.dir, tm.spider.projectName), 0755)
		if err != nil {
			return err
		}
	} else {
		err := os.MkdirAll(tm.dir, 0755)
		if err != nil {
			return err
		}
	}

	return nil
}

func (tm *monkeyTestContext) Run(t *testing.T) {
	defer func() {
		if tm.cleanUp != nil {
			assert.NilError(t, tm.cleanUp())
		}
		pterm.Info.Printfln("%s is done.", tm.name)
	}()

	tm.setParallel(t)

	// Call a function to clear vars and lock
	// TODO do this better, as of 1.20 we can test coverage with a binary rather than this hack
	deferment := takeCover()
	defer deferment()

	err := tm.getOrCreateDir()
	assert.NilError(t, err)

	// Remove old test files, create the new directories, and test tm values for relative directories
	err = tm.refreshTestFiles()
	assert.NilError(t, err)

	// Write the test config based on a spider supplied config method
	err = tm.spider.writeConfig(tm.dir, tm.configLoc)
	assert.NilError(t, err)

	// Write the session dir for tests in which values will not be set, but seer still needs to open
	err = os.Mkdir(tm.sessionLoc, common.DefaultDirPermission)
	assert.NilError(t, err)

	// Set selected project in the session
	err = tm.setSessionProject()
	assert.NilError(t, err)

	// Call a provided method to write files to the temp directory
	if tm.writeFilesInDir != nil {
		tm.writeFilesInDir(tm.dir)
	}

	// Make sure env is not nil
	if tm.env == nil {
		tm.env = make(map[string]string, 0)
	}

	// Set the default wait time if none is set
	if tm.waitTime == 0 {
		tm.waitTime = 30 * time.Second
	}

	// Start the mock server and get the url
	if tm.mock {
		// TODO dreamland

		// // get a random port 1024 to 65353 and start it
		port := fmt.Sprintf("%v", random.Intn(64329)+1024)
		tm.authUrl = "http://localhost:" + port
		mockServerStop := startMockOnPort(port)

		// wait for mock server to start
		// TODO, check ping the port
		time.Sleep(1500 * time.Millisecond)

		if tm.debug {
			fmt.Println("Started mock on", tm.authUrl)
		}
		defer func() {
			mockServerStop()
			// Give the server a moment to shut down
			time.Sleep(500 * time.Millisecond)
		}()
	} else {
		tm.authUrl = "https://auth.tau.sandbox.taubyte.com"
	}

	rr := roadRunner{
		configFile:  tm.configLoc,
		sessionFile: tm.sessionLoc,
		authUrl:     tm.authUrl,
		waitTime:    tm.waitTime,
		env:         tm.env,
		dir:         tm.dir,
	}

	// run preRun commands
	tm.runPreRun(t, rr)

	tm.runBabyMonkeys(t, rr)

	// run main command
	newMonkeyRunContext(tm.testMonkey, rr, false).Run(t)

	// Cleanup
	if !tm.spider.debug {
		os.Remove(tm.configLoc)
		os.RemoveAll(tm.dir)
	}
}

func (tm *monkeyTestContext) setSessionProject() error {
	projectName := tm.spider.projectName

	if projectName == "" {
		return nil
	}
	sessionLock.Lock()
	defer sessionLock.Unlock()

	err := session.LoadSessionInDir(tm.sessionLoc)
	if err != nil {
		return fmt.Errorf("loading session at `%s` failed with: %s", tm.sessionLoc, err)
	}

	err = session.Set().SelectedProject(projectName)
	if err != nil {
		return fmt.Errorf("setting selected project to `%s` failed with: %s", projectName, err)
	}

	return nil
}

func (tm *monkeyTestContext) runPreRun(t *testing.T, rr roadRunner) {
	runBefore := tm.spider.getBeforeEach(tm.testMonkey)

	if tm.preRun != nil {
		runBefore = append(runBefore, tm.preRun...)
	}
	for _, _args := range runBefore {
		out1, out2, exitCode, err := rr.Run(_args...)
		if err != nil {
			t.Errorf("RunBefore(%s) failed with error: %s \nOut1 %s:\n %s\nOut2:%s \n(%s)", _args, err, rr.dir, out1, out2, tm.name)
			return
		}

		if tm.debug {
			pterm.FgLightYellow.Println("RunBefore args:")
			fmt.Print(cleanArgs(_args), "\n\n")

			pterm.FgLightYellow.Print("RunBefore exitCode: ")
			fmt.Print(exitCode, "\n\n")

			pterm.FgLightYellow.Println("RunBefore Out:")
			fmt.Println(out1)

			pterm.FgLightYellow.Println("RunBefore Error:")
			fmt.Println(out2)

			fmt.Print(strings.Repeat("-", 50), "\n\n")
		}
	}
}
