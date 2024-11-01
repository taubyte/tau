package tests

import (
	"fmt"
	"testing"

	"github.com/taubyte/tau/tools/tau/constants"
	messagingPrompts "github.com/taubyte/tau/tools/tau/prompts/messaging"
)

func TestMessagingAll(t *testing.T) {
	runTests(t, createMessagingMonkey(), true)
}

func createMessagingMonkey() *testSpider {
	// Define shared variables
	command := "messaging"
	profileName := "test"
	projectName := "test_project"
	testName := "someMessaging"

	// Create a basic resource of name
	basicNew := func(name string) []string {
		return []string{
			"new", "-y", command,
			"--name", name,
			"--description", "some messaging description",
			"--tags", "tag1, tag2,   tag3",
			"--match", "xpath",
			"--no-local",
			"--no-mqtt",
			"--no-ws",
			"--no-regex",
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
				"some messaging description",
				"tag1", "tag2", "tag3",
				"false", "xpath",
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
				"some new messaging description",
				"tag1", "tag2", "tag341",
				"true", "xpdsaath",
			},
			preRun: [][]string{
				basicNew(testName),
			},
			children: []testMonkey{
				{
					name: "Edit basic",
					args: []string{"edit", "-y", command, "--name", testName,
						"--description", "some new messaging description",
						"--tags", "tag1, tag2,   tag341",
						"--local",
						"--regex",
						"--match", "xpdsaath",
						"--mqtt",
						"--web-socket",
					},
					wantOut: []string{command, testName, "Edited"},
				},
			},
		},
		{
			name:     "Query delete",
			args:     []string{"query", command, testName},
			exitCode: 1,
			errOut:   []string{fmt.Sprintf(messagingPrompts.NotFound, testName)},
			preRun: [][]string{
				basicNew(testName),
				{"delete", "-y", command, "--name", testName},
			},
		},
		{
			name: "Query list",
			args: []string{"query", command, "--list"},
			wantOut: []string{
				"someMsging1",
				"someMsging2",
				// "someMsging3", deleted
				"someMsging4",
				"someMsging5",
			},
			dontWantOut: []string{
				"someMsging3",
			},
			preRun: [][]string{
				basicNew("someMsging1"),
				basicNew("someMsging2"),
				basicNew("someMsging3"),
				{"delete", "-y", command, "--name", "someMsging3"},
				basicNew("someMsging4"),
				basicNew("someMsging5"),
			},
		},
		{
			name:     "Query New no -y",
			args:     []string{"query", command, testName},
			exitCode: 1,
			errOut:   []string{fmt.Sprintf(messagingPrompts.NotFound, testName)},
			children: []testMonkey{
				{
					name: "new no -y",
					args: []string{
						"new", command, "--name", testName,
						"--description", "some messaging description",
						"--tags", "tag1, tag2,   tag3",
						"--match", "xpath",
						"--no-local",
						"--no-mqtt",
						"--no-web-socket",
						"--no-regex",
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
				"some new messaging description",
				"tag341",
				"true", "xpdsaath",
			},
			preRun: [][]string{
				basicNew(testName),
			},
			children: []testMonkey{
				{
					name: "Edit no -y",
					args: []string{"edit", command, "--name", testName,
						"--description", "some new messaging description",
						"--tags", "tag1, tag2,   tag341",
						"--local",
						"--regex",
						"--match", "xpdsaath",
						"--mqtt",
						"--web-socket",
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
				"some messaging description",
				"tag1", "tag2", "tag3",
				"false", "xpath",
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
				"some new messaging description",
				"tag1", "tag2", "tag341",
				"true", "xpdsaath",
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
						"--description", "some new messaging description",
						"--tags", "tag1, tag2,   tag341",
						"--local",
						"--regex",
						"--match", "xpdsaath",
						"--mqtt",
						"--web-socket",
					},
				},
			},
		},
		{
			name:     "Delete with selected application",
			args:     []string{"query", command, testName},
			exitCode: 1,
			errOut:   []string{fmt.Sprintf(messagingPrompts.NotFound, testName)},
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
	return &testSpider{projectName, tests, beforeEach, getConfigString, "messaging"}
}
