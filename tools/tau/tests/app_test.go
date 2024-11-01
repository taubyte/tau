package tests

import (
	"fmt"
	"testing"

	"github.com/taubyte/tau/tools/tau/constants"
	applicationPrompts "github.com/taubyte/tau/tools/tau/prompts/application"
)

func TestApplicationAll(t *testing.T) {
	runTests(t, createApplicationMonkey(), true)
}

func createApplicationMonkey() *testSpider {

	// Define shared variables
	command := "application"
	profileName := "test"
	projectName := "test_project"
	testApplicationName := "someApp"

	// Define method for simple resource creation of name
	basicNew := func(name string) []string {
		return []string{
			"new", "-y", command,
			"--name", name,
			"--description", "some app desc",
			"--tags", "some, other, tags",

			"--color", "never",
		}
	}

	// The config that will be written
	getConfigString := basicGetConfigString(profileName, projectName)

	// Run before each test
	beforeEach := func(tt testMonkey) [][]string {
		tt.env[constants.CurrentProjectEnvVarName] = projectName
		return nil
	}

	// Define tests
	tests := []testMonkey{
		{
			name:    "New Basic",
			args:    []string{"query", command, "--name", testApplicationName},
			wantOut: []string{testApplicationName, "some app desc", "some", "other", "tags"},
			preRun:  [][]string{},
			children: []testMonkey{{
				name:    "new",
				args:    basicNew(testApplicationName),
				wantOut: []string{"Created application:", testApplicationName},
			}},
		},
		{
			name:     "New Basic no -y",
			args:     []string{"new", command, "--name", testApplicationName, "--description", "some app desc", "--tags", "some, other, tags"},
			exitCode: 2,
			errOut:   []string{"EOF"},
		},
		{
			name:    "Edit Basic",
			args:    []string{"query", command, "--name", testApplicationName},
			wantOut: []string{testApplicationName, "some nedwdadda", "some", "wack", "tags"},
			preRun: [][]string{
				basicNew(testApplicationName),
			},
			children: []testMonkey{{
				args: []string{
					"edit", "-y", command,
					"--name", testApplicationName,
					"--description", "some nedwdadda",
					"--tags", "some, wack, tags",
					"--color", "never",
				},
				wantOut: []string{"Edited application: " + testApplicationName},
			}},
		},
		{
			name:     "Edit Basic no -y",
			args:     []string{"edit", command, "--name", testApplicationName, "--description", "some nedwdadda", "--tags", "some, dsadsad, tags"},
			exitCode: 2,
			errOut:   []string{"EOF"},
			preRun: [][]string{
				basicNew(testApplicationName),
			},
		},
		{
			name:     "Delete Basic",
			args:     []string{"query", command, "--name", testApplicationName},
			exitCode: 1,
			errOut:   []string{fmt.Sprintf(applicationPrompts.NotFound, testApplicationName)},
			preRun: [][]string{
				basicNew(testApplicationName),
			},
			children: []testMonkey{{
				args: []string{
					"delete", "-y", command,
					"--name", testApplicationName,
					"--color", "never",
				},
				wantOut: []string{"Deleted application: " + testApplicationName},
			}},
		},
		{
			name:    "Delete Basic no -y",
			args:    []string{"query", command, "--name", testApplicationName},
			wantOut: []string{testApplicationName, "some app desc", "some", "other", "tags"},
			preRun: [][]string{
				basicNew(testApplicationName),
			},
			children: []testMonkey{
				{
					args:     []string{"delete", command, "--name", testApplicationName},
					exitCode: 2,
					errOut:   []string{"EOF"},
				},
			},
		},
		{
			name:        "Query",
			args:        []string{"query", command, "--list"},
			wantOut:     []string{"someapp1", "someapp2", "someapp3", "someapp4", "someapp5"},
			dontWantOut: []string{"someapp13"},
			preRun: [][]string{
				basicNew("someapp1"),
				basicNew("someapp2"),
				basicNew("someapp3"),
				basicNew("someapp13"),
				{"delete", "-y", command, "--name", "someapp13"},
				basicNew("someapp4"),
				basicNew("someapp5"),
			},
			env: map[string]string{
				constants.CurrentApplicationEnvVarName: "",
			},
		},
		{
			name:    "Select from new",
			args:    basicNew(testApplicationName),
			wantOut: []string{"Selected application:", testApplicationName},
		},
		{
			name:    "Select from created",
			args:    []string{"select", command, "--name", "someapp1", "--color", "never"},
			wantOut: []string{"Selected application: someapp1"},
			preRun: [][]string{
				basicNew("someapp1"),
				basicNew("someapp2"),
			},
		},
		{
			name:     "Select non exsistant",
			args:     []string{"select", command, "--name", "somenoneapp1"},
			exitCode: 1,
			errOut:   []string{"application `somenoneapp1` not found"},
			preRun: [][]string{
				basicNew("someapp1"),
				basicNew("someapp2"),
			},
		},
	}
	return &testSpider{projectName, tests, beforeEach, getConfigString, "application"}
}
