package tests

import (
	"time"

	"github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/tools/tau/singletons/session"
)

type testMonkey struct {
	// name:
	// The name of the test as shown in the
	// Example:
	// output Test/name/some_basic_test
	name string

	// waitTime: (default: 5*time.Second)
	// the time the test will run until it
	// is forcefully killed, and causes
	// the test to exit in failure
	waitTime time.Duration

	// args:
	// an array of arguments to run with tau
	// Example:
	// args = {"new", "app"}
	// on command line `tau new app`
	args []string

	// wantOut:
	// the test will fail is all of the
	// provided strings are not found in
	// the stdOut of the command
	wantOut []string

	// dontWantOut:
	// the test will fail if any of the
	// provided strings are found in
	// the stdOut of the command
	dontWantOut []string

	// preRun:
	// a 2D array of arguments to run
	// before the test is ran this runs
	// after the spider's beforeEach method
	preRun [][]string

	// children:
	//
	// An array of monkeys that will be ran
	// before the monkey.
	// NOTE: there is a validation where if you use
	// arguments that are not used by the baby monkey
	// errors will be thrown
	// uses the following arguments:
	// name:
	// args:
	// debug: All siblings will still run
	// errOut:
	// exitCode:
	// wantOut:
	// dontWantOut:
	// wantDir:
	// dontWantDir
	children []testMonkey

	// env:
	// a map of environment variables
	// to run with the given command
	// this will override environment variables
	// set in the spider's beforeEach method
	env map[string]string

	// exitCode:
	// the exit code to expect by
	// the test's completion
	// Generally: 1=fail, 0=success
	exitCode int

	// errOut:
	// Expect error output to contain
	errOut []string

	// mock:
	// if true runs a mock server on a random
	// port between 1024, 65353
	// command to find where mock server running
	// netstat -nlp|grep python3
	mock bool

	// writeFilesInDir:
	// A method used to write files to the
	// temp directory before the test is ran
	writeFilesInDir func(dir string)

	// debug:
	// if true prints the stdOut and stdErr
	// and only runs tests marked as debug
	// it will also run the test in
	// _fakeroot/debug/<testname> rather
	// than the temp directory
	//
	// more on debug:
	// A test will say "Started
	// mock on http://localhost:48621"
	// simply run the mock server on that port
	// and view the log if interested
	// generally it will be `python3 tests/mock 48621`
	//
	// Why 48621 ? because that's the first number the
	// random seed 42 gives.  If you're debugging multiple
	// tests at once you'll need to worry about
	// different ports.  if those tests are running in
	// parallel... good luck, it's quite random
	debug bool

	// wantDir:
	// the test will fail if any of the
	// provided directories are not found
	wantDir []string

	// dontWantDir:
	// the test will fail if any of the
	// provided directories are found
	dontWantDir []string

	// evaluateSession:
	// a function that analyzes the getter and returns an error
	// if something is not right
	evaluateSession func(g session.Getter) error

	// cleanUp:
	// runs after the test to clean artifacts
	cleanUp func() error

	// confirmProject
	// gives the test a schema of the project for confirming values in schema
	confirmProject func(project.Project) error
}

type testSpider struct {
	// projectName (optional):
	// If provided creates the relative code and
	// config directories of the project
	projectName string

	// tests:
	// ran in parallel with their own
	// environment and directory tree
	tests []testMonkey

	// beforeEach:
	// a method that runs before each test
	// set environment variables inside and,
	// return a 2D array of arguments to run commands
	// before the rest of the commands are ran
	// beforeEach := func(tt testMonkey) [][]string {
	// 	tt.env[constants.envVarX] = projectName
	// 	return [][]string{basicNew(testName)}
	// }
	beforeEach func(tt testMonkey) [][]string

	// getConfigString:
	// writes the bytes returned to the method
	// to the directory provided/tau.yaml
	// dir can also be used to set variables
	// relative to the config directory
	// i.e project_path
	getConfigString func(dir string) []byte

	testName string
}

type roadRunner struct {
	// configFile:
	// the path to the config file
	// Example:
	// "_fakeroot/temp319031/tau.yaml"
	configFile string

	// sessionFile:
	// the path to the session file
	sessionFile string

	// authUrl:
	// the url to the auth server
	// Example:
	// "http://localhost:48621"
	authUrl string

	// waitTime:
	// the time the test will run until it
	// is forcefully killed, and causes
	// the test to exit in failure
	waitTime time.Duration

	// env:
	// a map of environment variables
	// to run with the given command
	// this will override environment variables
	// set in a spider's beforeEach method
	// Example:
	// {EnvVarNameTest: "test"}
	env map[string]string

	// dir:
	// the directory to run the tests in
	// Example:
	// "_fakeroot/temp319031"
	dir string
}
