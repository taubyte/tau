package tests

import (
	"fmt"
	"testing"

	"github.com/taubyte/tau/tools/tau/constants"
	smartopsPrompts "github.com/taubyte/tau/tools/tau/prompts/smartops"
)

// Define a method to test your monkey
func TestSmartopsAll(t *testing.T) {
	runTests(t, createSmartopsMonkey(), true)
}

func createSmartopsMonkey() *testSpider {

	// Define shared variables
	command := "smartops"
	profileName := "test"
	projectName := "test_project"
	testName := "test_smartops"

	basicNewNoTemplate := func(name string) []string {
		return []string{
			"new", "-y", command,
			"--name", name,
			"--description", "some smartops description",
			"--tags", "tag1, tag2,   tag3",
			"--ttl", "10s",
			"--memory", "10",
			"--memory-unit", "GB",
			"--no-use-template",
			"--source", ".",
			"--call", "ping",
		}
	}

	testDirPrefix := "test_project/code/smartops/test_smartops/"

	testLibraryName := "test_library"

	// The config that will be written
	getConfigString := basicGetConfigString(profileName, projectName)

	// Run before each test
	beforeEach := func(tt testMonkey) [][]string {
		tt.env[constants.CurrentProjectEnvVarName] = projectName
		return nil
	}

	// Define test monkeys that will run in parallel
	tests := []testMonkey{
		{
			name: "Query New basic no template",
			args: []string{"query", command, testName},
			wantOut: []string{
				"tag1", "tag2", "tag3", testName,
				"Source      │ .",
				"Call        │ ping",
			},
			wantDir: []string{
				"test_project/code/smartops/test_smartops/test_smartops.md",
			},
			children: []testMonkey{
				{
					args:    basicNewNoTemplate(testName),
					wantOut: []string{command, testName, "Created"},
				},
			},
		},
		{
			name: "Query New basic template",
			args: []string{"query", command, testName},
			wantOut: []string{
				"tag1", "tag2", "tag3", testName,
				"Source      │ .",
				"Call        │ confirmHttp",
			},
			wantDir: []string{
				testDirPrefix + "confirm_http.go",
				testDirPrefix + "go.mod",
				testDirPrefix + ".taubyte/build.sh",
				testDirPrefix + ".taubyte/config.yaml",
			},
			children: []testMonkey{
				{
					args: []string{
						"new", "-y", command,
						"--name", testName,
						"--description", "some smartops description",
						"--tags", "tag1, tag2,   tag3",
						"--ttl", "10s",
						"--memory", "10",
						"--memory-unit", "GB",
						"--use-template",
						"--template", "confirm_http",
						"--lang", "go",
						"--source", ".",
						"--call", "confirmHttp",
					},
					wantOut: []string{command, testName, "Created"},
				},
			},
		},
		{
			name: "Query Edit basic no template",
			args: []string{"query", command, testName},
			wantOut: []string{
				"tag4", testName,
				"Source      │ libraries/test_library",
				"Call        │ test_library.ping",
				"10m",
				"50MB",
			},
			wantDir: []string{
				"test_project/code/smartops/test_smartops/test_smartops.md",
			},
			preRun: [][]string{
				basicNewLibrary(testLibraryName),
			},
			children: []testMonkey{
				{
					args:    basicNewNoTemplate(testName),
					wantOut: []string{command, testName, "Created"},
				},
				{
					args: []string{
						"edit", "-y", command, "--name", testName,
						"--description", "some smartops description",
						"--tags", "tag4",
						"--ttl", "10m",
						"--memory", "50",
						"--memory-unit", "MB",
						"--source", "test_library",
						"--call", "test_library.ping",
					},
					wantOut: []string{command, testName, "Edited"},
				},
			},
		},
		{
			name:     "Query delete",
			args:     []string{"query", command, testName},
			exitCode: 1,
			errOut:   []string{fmt.Sprintf(smartopsPrompts.NotFound, testName)},
			preRun: [][]string{
				basicNewNoTemplate(testName),
			},
			children: []testMonkey{
				{
					name:    "Delete basic",
					args:    []string{"delete", "-y", command, "--name", testName},
					wantOut: []string{command, testName, "Deleted"},
				},
			},
		},
		{
			name: "Query list",
			args: []string{"query", command, "--list"},
			wantOut: []string{
				testName + "1",
				testName + "2",
				// testName+"3", deleted
				testName + "4",
				testName + "5",
			},
			dontWantOut: []string{
				testName + "3",
			},
			preRun: [][]string{
				basicNewNoTemplate(testName + "1"),
				basicNewNoTemplate(testName + "2"),
				basicNewNoTemplate(testName + "3"),
				{"delete", "-y", command, "--name", testName + "3"},
				basicNewNoTemplate(testName + "4"),
				basicNewNoTemplate(testName + "5"),
			},
		},
	}
	return &testSpider{projectName, tests, beforeEach, getConfigString, "smartops"}
}
