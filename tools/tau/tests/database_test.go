package tests

import (
	"fmt"
	"testing"

	"github.com/taubyte/tau/tools/tau/constants"
	databasePrompts "github.com/taubyte/tau/tools/tau/prompts/database"
)

// Define a method to test your monkey
func TestDatabaseAll(t *testing.T) {
	runTests(t, createDatabaseMonkey(), true)
}

func createDatabaseMonkey() *testSpider {

	// Define shared variables
	command := "database"
	profileName := "test"
	projectName := "test_project"
	testName := "someDB"

	// Create a basic resource of name
	basicNew := func(name string) []string {
		return []string{
			"new", "-y", command,
			"--name", name,
			"--description", "some database description",
			"--tags", "tag1, tag2,   tag3",
			"--no-local",
			"--encryption",
			"--match", "someMatch",
			"--no-regex",
			"--key", "somekey",
			"--min", "10",
			"--max", "112",
			"--size", "10",
			"--size-unit", "GB",
		}
	}

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
			name: "Query new",
			args: []string{"query", command, testName},
			wantOut: []string{
				testName,
				"some database description",
				"tag1", "tag2", "tag3",
				"all", "10", "112", "10GB",
				"true",
			},
			children: []testMonkey{
				{
					name:    "New basic",
					args:    basicNew(testName),
					wantOut: []string{command, testName, "Created"},
				},
			},
		},
		{
			name: "Query edit",
			args: []string{"query", command, testName},
			wantOut: []string{
				testName,
				"some database description",
				"tag1", "tag2", "tag3",
				"host", "10", "12", "200PB",
				"false",
			},
			preRun: [][]string{
				basicNew(testName),
			},
			children: []testMonkey{
				{
					name: "Edit basic",
					args: []string{"edit", "-y", command, "--name", testName,
						"--description", "some database description",
						"--tags", "tag1, tag2,   tag3",
						"--local",
						"--no-encryption",
						"--no-regex",
						"--match", "test",
						"--min", "10",
						"--max", "12",
						"--size", "200PB",
					},
					wantOut: []string{command, testName, "Edited"},
				},
			},
		},
		{
			name:     "Query delete",
			args:     []string{"query", command, testName},
			exitCode: 1,
			errOut:   []string{fmt.Sprintf(databasePrompts.NotFound, testName)},
			preRun: [][]string{
				basicNew(testName),
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
				"SomeDB1",
				"SomeDB2",
				// "SomeDB3", deleted
				"SomeDB4",
				"SomeDB5",
			},
			dontWantOut: []string{
				"SomeDB3",
			},
			preRun: [][]string{
				basicNew("SomeDB1"),
				basicNew("SomeDB2"),
				basicNew("SomeDB3"),
				{"delete", "-y", command, "--name", "SomeDB3"},
				basicNew("SomeDB4"),
				basicNew("SomeDB5"),
			},
		},
		{
			name:     "Query New no -y",
			args:     []string{"query", command, testName},
			exitCode: 1,
			errOut:   []string{fmt.Sprintf(databasePrompts.NotFound, testName)},
			children: []testMonkey{
				{
					name: "new no -y",
					args: []string{
						"new", command, "--name", testName,
						"--description", "some database description",
						"--tags", "tag1, tag2,   tag3",
						"--no-local",
						"--regex",
						"--encryption",
						"--key", "somekey",
						"--min", "10",
						"--max", "112",
						"--match", "testmatch",
						"--size", "10",
						"--size-unit", "GB",
					},
					exitCode: 2,
					errOut:   []string{"EOF"},
				},
			},
		},
		{
			name: "Query Edit no -y",
			args: []string{"query", command, testName},
			dontWantOut: []string{
				"false", "SOMENEWPATH", "tag28913",
				"42", "432", "43GB", "hola",
			},
			preRun: [][]string{
				basicNew(testName),
			},
			children: []testMonkey{
				{
					name: "Edit no -y",
					args: []string{"edit", command, "--name", testName,
						"--description", "some hola description",
						"--tags", "tag1, tag28913,   tag3",
						"-local",
						"--no-encryption",
						"--regex",
						"--min", "42",
						"--max", "432",
						"--match", "somematch",
						"--size", "43GB",
					},
					exitCode: 2,
					errOut:   []string{"EOF"},
				},
			},
		},
		{
			name: "New with selected application",
			args: []string{"query", command, testName},
			wantOut: []string{
				testName,
				"some database description",
				"tag1", "tag2", "tag3",
				"all", "10", "112", "10GB",
				"true",
			},
			preRun: [][]string{
				basicNew(testName),
			},
			env: map[string]string{
				constants.CurrentApplicationEnvVarName: "someasdasdapp",
			},
		},
		{
			name: "Edit with selected application",
			args: []string{"query", command, testName},
			wantOut: []string{
				testName,
				"some hola description",
				"tag1", "tag28913", "tag3",
				"host", "42", "432", "43GB",
				"false",
			},
			preRun: [][]string{
				basicNew(testName),
			},
			env: map[string]string{
				constants.CurrentApplicationEnvVarName: "someasdasdapp",
			},
			children: []testMonkey{
				{
					name: "Edit with selected application",
					args: []string{"edit", "-y", command, "--name", testName,
						"--description", "some hola description",
						"--tags", "tag1, tag28913,   tag3",
						"-local",
						"--no-encryption",
						"--regex",
						"--match", "someMatch",
						"--min", "42",
						"--max", "432",
						"--size", "43GB",
					},
				},
			},
		},
		{
			name:     "Delete with selected application",
			args:     []string{"query", command, testName},
			exitCode: 1,
			errOut:   []string{fmt.Sprintf(databasePrompts.NotFound, testName)},
			preRun: [][]string{
				basicNew(testName),
			},
			env: map[string]string{
				constants.CurrentApplicationEnvVarName: "someasdasdapp",
			},
			children: []testMonkey{
				{
					name: "Delete with selected application",
					args: []string{"delete", "-y", command, "--name", testName},
				},
			},
		},
	}
	return &testSpider{projectName, tests, beforeEach, getConfigString, "database"}
}
