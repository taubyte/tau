package tests

import (
	"fmt"
	"testing"

	"github.com/taubyte/tau/tools/tau/constants"
)

func TestServiceAll(t *testing.T) {
	runTests(t, createServiceMonkey(), true)
}

func basicNewService(name string, protocol string) []string {
	return []string{
		"new", "-y", "service",
		"--name", name,
		"--description", "some service description",
		"--tags", "tag1, tag2,   tag3",
		"--protocol", protocol,

		"--color", "never",
	}
}

func createServiceMonkey() *testSpider {

	// Define shared variables
	// singularCamelCase := "Service"
	camelCommand := "Service"
	testName := "someService"
	testProtocol := "/testprotocol/v1"
	profileName := "test"
	projectName := "test_project"
	appName := "someApp"
	command := "service"
	testAppDir := fmt.Sprintf("test_project/config/applications/%s/services/%s.yaml", appName, testName)

	// Create a basic resource of name
	basicNew := basicNewService

	// The config that will be written
	writeFilesInDir := basicGetConfigString(profileName, projectName)

	// Run before each test
	beforeEach := func(tt testMonkey) [][]string {
		tt.env[constants.CurrentProjectEnvVarName] = projectName
		return nil
	}

	// Define test monkeys that will run in parallel
	tests := []testMonkey{
		{
			name:    "New basic",
			args:    basicNew(testName, testProtocol),
			wantOut: []string{"Created", camelCommand, testName},
		},
		{
			name: "New basic wrong service name",
			args: []string{
				"new", command,
				"--name", testName,
				"--description", "some service description",
				"--tags", "tag1, tag2,   tag3",
				"--protocol", "/testprotocol/v1",
			},
			exitCode: 2,
			errOut:   []string{"EOF"},
		},
		{
			name:    "New basic with app",
			args:    basicNew(testName, testProtocol),
			wantOut: []string{camelCommand, testName, "Created"},
			wantDir: []string{
				testAppDir,
			},
			env: map[string]string{
				constants.CurrentApplicationEnvVarName: appName,
			},
		},
		{
			name: "New basic no -y",
			args: []string{
				"new", command,
				"--name", testName,
				"--description", "some service description",
				"--tags", "tag1, tag2,   tag3",
				"--protocol", "/testprotocol/v1",
			},
			exitCode: 2,
			errOut:   []string{"EOF"},
		},
		{
			name: "Edit basic",
			args: []string{
				"edit", "-y", command,
				"--name", testName,
				"--description", "some newwenwenwenwen description",
				"--tags", "tag1, tag23,   tag3",
				"--protocol", "/testprotocol/v2",
			},
			wantOut: []string{camelCommand, testName, "Edited"},
			preRun: [][]string{
				basicNew(testName, testProtocol),
			},
		},
		{
			name:    "Edit basic query",
			args:    []string{"query", command, "--name", testName},
			wantOut: []string{"newwenwenwenwen", "tag23"},
			preRun: [][]string{
				basicNew(testName, testProtocol),
				{
					"edit", "-y", command,
					"--name", testName,
					"--description", "some newwenwenwenwen description",
					"--tags", "tag1, tag23,   tag3",
					"--protocol", "/testprotocol/v1",
				},
			},
		},
		{
			name:    "Delete basic",
			args:    []string{"delete", "-y", command, "--name", testName},
			wantOut: []string{camelCommand, testName, "Deleted"},
			preRun: [][]string{
				basicNew(testName, testProtocol),
			},
		},
		{
			name:     "Delete basic Query",
			args:     []string{"query", command, "--name", testName},
			errOut:   []string{fmt.Sprintf("service `%s` not found", testName)},
			exitCode: 1,
			preRun: [][]string{
				basicNew(testName, testProtocol),
				{"delete", "-y", command, "--name", testName},
			},
		},
		{
			name:    "Delete basic with selected app",
			args:    []string{"delete", "-y", command, "--name", testName},
			wantOut: []string{camelCommand, testName, "Deleted"},
			dontWantDir: []string{
				testAppDir,
			},
			env: map[string]string{
				constants.CurrentApplicationEnvVarName: appName,
			},
			preRun: [][]string{
				basicNew(testName, testProtocol),
			},
		},
		{
			name:        "Query",
			args:        []string{"query", command, "--list"},
			wantOut:     []string{"someService1", "someapp2", "someapp3", "someapp4", "someapp5"},
			dontWantOut: []string{"someapp13"},
			preRun: [][]string{
				basicNew("someService1", testProtocol),
				basicNew("someapp2", testProtocol),
				basicNew("someapp3", testProtocol),
				basicNew("someapp13", testProtocol),
				{"delete", "-y", command, "--name", "someapp13"},
				basicNew("someapp4", testProtocol),
				basicNew("someapp5", testProtocol),
			},
		},
	}
	return &testSpider{projectName, tests, beforeEach, writeFilesInDir, "service"}
}
