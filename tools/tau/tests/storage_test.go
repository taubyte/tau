package tests

import (
	"fmt"
	"testing"

	"github.com/taubyte/tau/tools/tau/constants"
	storagePrompts "github.com/taubyte/tau/tools/tau/prompts/storage"
)

func TestStorageAll(t *testing.T) {
	runTests(t, createStorageMonkey(), true)
}

func createStorageMonkey() *testSpider {
	// Define shared variables
	command := "storage"
	profileName := "test"
	projectName := "test_project"
	testName := "someStorage"

	// Create a basic resource of name
	basicNew := func(name string) []string {
		return []string{
			"new", "-y", command,
			"--name", name,
			"--description", "some storage description",
			"--tags", "tag1, tag2,   tag3",
			"--bucket", "Streaming",
			"--ttl", "20s",
			"--no-regex",
			"--match", "test/v1",
			"--public",
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
			name: "Simple new",
			args: []string{"query", command, testName},
			children: []testMonkey{
				{
					name: "New basic",
					args: []string{
						"new", "-y", command,
						"--name", testName,
						"--description", "some storage description",
						"--tags", "tag1, tag2,   tag3",
						"-bucket", "Streaming",
						"--ttl", "20s",
						"--no-regex",
						"--match", "test/v1",
						"-public",
						"-size", "10",
						"-size-unit", "GB",
					},
					wantOut: []string{command, testName, "Created"},
				},
			},
		},
		{
			name: "Query new",
			args: []string{"query", command, testName},
			wantOut: []string{
				testName,
				"some storage description",
				"tag1", "tag2", "tag3",
				"Streaming",
				"all", "10", "GB",
			},
			dontWantOut: []string{"true"},
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
				"some new storage description",
				"tag543", "tag422", "tag341",
				"Streaming", "all", "15", "KB",
			},
			preRun: [][]string{
				basicNew(testName),
			},
			children: []testMonkey{
				{
					name: "Edit basic",
					args: []string{"edit", "-y", command, "--name", testName,
						"--description", "some new storage description",
						"--tags", "tag543, tag422,   tag341",
						"--bucket", "Streaming",
						"--ttl", "25s",
						"--public",
						"--no-regex",
						"--size", "15",
						"--size-unit", "KB",
						"--match", "test/v1",
					},
					wantOut: []string{command, testName, "Edited"},
				},
			},
		},
		{
			name:     "Query delete",
			args:     []string{"query", command, testName},
			exitCode: 1,
			errOut:   []string{fmt.Sprintf(storagePrompts.NotFound, testName)},
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
				"someStrg1",
				"someStrg2",
				// "someStrg3", deleted
				"someStrg4",
				"someStrg5",
			},
			dontWantOut: []string{
				"someStrg3",
			},
			preRun: [][]string{
				basicNew("someStrg1"),
				basicNew("someStrg2"),
				basicNew("someStrg3"),
				{"delete", "-y", command, "--name", "someStrg3"},
				basicNew("someStrg4"),
				basicNew("someStrg5"),
			},
		},
		{
			name: "New with selected application",
			args: []string{"query", command, testName},
			wantOut: []string{
				testName,
				"some storage description",
				"tag1", "tag2", "tag3",
				"Streaming",
				"all", "10", "GB",
			},
			dontWantOut: []string{"true"},
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
				"some new storage description",
				"tag1", "tag2", "tag343",
				"host", "15", "KB",
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
						"--description", "some new storage description",
						"--tags", "tag1, tag2,   tag343",
						"--bucket", "Streaming",
						"--ttl", "25s",
						"--no-public",
						"--no-regex",
						"--match", "some/match",
						"--size", "15",
						"--size-unit", "KB",
					},
				},
			},
		},
		{
			name:     "Delete with selected application",
			args:     []string{"query", command, testName},
			exitCode: 1,
			errOut:   []string{fmt.Sprintf(storagePrompts.NotFound, testName)},
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
		{
			name: "Differencing New Streaming",
			args: []string{"query", command, testName},
			wantOut: []string{
				testName,
				"some storage description",
				"tag1", "tag2", "tag3",
				"Streaming",
				"all", "10GB",
			},
			dontWantOut: []string{"true"},
			preRun: [][]string{
				basicNew(testName),
			},
		},
		{
			name: "Differencing New Object",
			args: []string{"query", command, testName},
			wantOut: []string{
				testName,
				"some storage description",
				"tag1", "tag2", "tag3",
				"Object", "Versioning",
				"all", "10", "GB",
			},
			dontWantOut: []string{"TTL"},
			preRun: [][]string{
				{"new", "-y", command, "--name", testName,
					"--description", "some storage description",
					"--tags", "tag1, tag2,   tag3",
					"--bucket", "Object",
					"--public",
					"--versioning",
					"--no-regex",
					"--match", "some/match",
					"--size", "10",
					"--size-unit", "GB",
				},
			},
		},
		{
			name: "Differencing Edit Object to Streaming",
			args: []string{"query", command, testName},
			wantOut: []string{
				testName,
				"some new storage description",
				"tag1", "tag2", "tag3",
				"Streaming", "TTL",
				"all", "10", "GB",
			},
			dontWantOut: []string{"Versioning"},
			preRun: [][]string{
				{"new", "-y", command, "--name", testName,
					"--description", "some new storage description",
					"--tags", "tag1, tag2,   tag3",
					"--bucket", "Object",
					"--no-public",
					"--versioning",
					"--no-regex",
					"--match", "some/match",
					"--size", "10",
					"--size-unit", "GB",
				},
				{"edit", "-y", command, "--name", testName,
					"--description", "some new storage description",
					"--tags", "tag1, tag2,   tag3",
					"--bucket", "Streaming",
					"--ttl", "25s",
					"--public",
					"--no-regex",
					"--match", "some/match",
					"--size", "10",
					"--size-unit", "GB",
				},
			},
		},
		{
			name: "Differencing Edit Streaming to Object",
			args: []string{"query", command, testName},
			wantOut: []string{
				testName,
				"some new storage description",
				"tag1", "tag2", "tag3",
				"Object", "Versioning",
				"all", "10", "GB",
			},
			dontWantOut: []string{"TTL"},
			preRun: [][]string{
				basicNew(testName),
				{"edit", "-y", command, "--name", testName,
					"--description", "some new storage description",
					"--tags", "tag1, tag2,   tag3",
					"--bucket", "Object",
					"--public",
					"--versioning",
					"--no-regex",
					"--match", "some/match",
					"--size", "10",
					"--size-unit", "GB",
				},
			},
		},
	}
	return &testSpider{projectName, tests, beforeEach, getConfigString, "storage"}

}
