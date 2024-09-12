package tests

import (
	"fmt"
	"testing"
)

func TestAuthAll(t *testing.T) {
	runTests(t, createAuthMonkey(t), true)
}

func createAuthMonkey(t *testing.T) *testSpider {
	command := "login"

	testProfileName := "someProfile"
	basicNew := func(name string) []string {
		return []string{
			command, testProfileName,
			"-p", Provider,
			"-t", Token(t),

			// Disable output color
			"--color", "never",
		}
	}

	tests := []testMonkey{
		{
			name: "Creating new profile",
			args: basicNew(testProfileName),
			wantOut: []string{
				fmt.Sprintf("Created default profile: %s", testProfileName),
			},
			evaluateSession: expectProfileName(testProfileName),
		},
		{
			name: "Creating default profile",
			args: []string{
				command, testProfileName + "2",
				"-p", Provider,
				"-t", Token(t),
				"--new",
				"--set-default",

				// Disable output color
				"--color", "never",
			},
			wantOut: []string{
				fmt.Sprintf("Created default profile: %s", testProfileName+"2"),
			},
			preRun: [][]string{
				basicNew(testProfileName),
			},
			evaluateSession: expectProfileName(testProfileName + "2"),
		},
		{
			name: "env should export and not set",
			args: append(basicNew(testProfileName), "--env"),
			wantOut: []string{
				fmt.Sprintf("Created default profile: %s", testProfileName),
				fmt.Sprintf("export TAUBYTE_PROFILE=%s", testProfileName),
			},
			evaluateSession: expectProfileName(""),
		},
		{
			name: "should select",
			args: []string{
				command, testProfileName,

				// Disable output color
				"--color", "never",
			},
			wantOut: []string{
				fmt.Sprintf("Selected profile: %s", testProfileName),
			},
			preRun: [][]string{
				basicNew(testProfileName),
				basicNew(testProfileName + "2"),
			},
			evaluateSession: expectProfileName(testProfileName),
		},
	}
	return &testSpider{"some_project", tests, nil, nil, "login"}
}
