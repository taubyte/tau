package tests

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/google/go-github/v53/github"
	commonTest "github.com/taubyte/tau/tools/tau/common/test"
	"github.com/taubyte/tau/tools/tau/constants"
	libraryPrompts "github.com/taubyte/tau/tools/tau/prompts/library"
	"golang.org/x/oauth2"
)

func basicNewLibrary(name string) []string {
	return []string{
		"new", "-y", "library",
		"--name", name,
		"--description", "some library description",
		"--tags", "tag1, tag2,   tag3",
		"--no-generate-repository",
		"--path", "/",
		"--repository-name", "tb_website_reactdemo",
		"--repository-id", "123456",
		"--no-clone",
		"--branch", "master",
		"--provider", "github",
	}
}

func TestLibraryAll(t *testing.T) {
	t.Skip("authNodeUrl - use dream instead")
	runTests(t, createLibraryMonkey(t), true)
}

func createLibraryMonkey(t *testing.T) *testSpider {
	// Define shared variables
	command := "library"
	profileName := "test"
	projectName := "test_project"
	testName := "someLibrary"
	network := "Test"

	// Create a basic resource of name
	basicNew := func(name string) []string {
		return []string{
			"new", "-y", command,
			"--name", name,
			"--description", "some library description",
			"--tags", "tag1, tag2,   tag3",
			"--no-generate-repository",
			"--path", "/",
			"--repository-name", "tb_website_reactdemo",
			"--no-clone",
			"--branch", "master",
			"--provider", "github",
		}
	}

	// The config that will be written
	getConfigString := basicValidConfigString(t, profileName, projectName)

	// Run before each test
	beforeEach := func(tt testMonkey) [][]string {
		tt.env[constants.CurrentProjectEnvVarName] = projectName
		tt.env[constants.CurrentSelectedNetworkName] = network
		return nil
	}

	// Define test monkeys that will run in parallel
	tests := []testMonkey{
		{
			name: "Simple new",
			args: []string{
				"new", "-y", command,
				"--name", testName,
				"--description", "some library description",
				"--tags", "tag1, tag2,   tag3",
				"--generate-repository",
				"--private",
				"--template", "empty",
				"--branch", "master",
				"--path", "/",
				"--provider", "github",
				"--no-embed-token",
			},
			cleanUp: func() error {
				ts := oauth2.StaticTokenSource(
					&oauth2.Token{AccessToken: commonTest.GitToken(t)},
				)
				tc := oauth2.NewClient(context.Background(), ts)
				client := github.NewClient(tc)

				resp, err := client.Repositories.Delete(context.Background(), commonTest.GitUser, "tb_library_someLibrary")
				if err != nil {
					return fmt.Errorf("req do failed with: %w", err)
				}
				if resp != nil {
					defer resp.Body.Close()

					if err != nil {
						body, err := io.ReadAll(resp.Body)
						if err != nil {
							return nil
						}
						fmt.Println("Delete repository response", string(body))
					}
				}

				return nil
			},
		},
		{
			mock: true,
			name: "New from current repository",
			args: []string{
				"query", command, testName,
			},
			wantOut: []string{"tb_website_reactdemo", "github", "master"},
			preRun: [][]string{
				basicNew(testName),
			},
		},
		{
			mock: true,
			name: "edit basic",
			args: []string{
				"query", command, testName,
			},
			wantOut: []string{"/new"},
			preRun: [][]string{
				basicNew(testName),
			},
			children: []testMonkey{
				{
					name: "edit",
					args: []string{
						"edit", "-y", command, testName,
						"--description", "some new library description",
						"--tags", "tag1, tag2,   tag4",
						"--path", "/new",
						"--no-clone",
						"--branch", "master",
						"--domains", "hal.computers.com",
					},
					wantOut: []string{"Edited", "library", testName},
				},
			},
		},
		{
			mock: true,
			name: "delete basic",
			args: []string{
				"query", command, testName,
			},
			exitCode: 1,
			errOut:   []string{fmt.Sprintf(libraryPrompts.NotFound, testName)},
			preRun: [][]string{
				basicNew(testName),
			},
			children: []testMonkey{
				{
					name: "delete",
					args: []string{
						"delete", "-y", command, testName,
					},
				},
			},
		},
		{
			mock: true,
			name: "Query list",
			args: []string{"query", command, "--list"},
			wantOut: []string{
				testName + "1",
				testName + "2",
				// testName + "3", deleted
				testName + "4",
				testName + "5",
			},
			dontWantOut: []string{
				testName + "3",
			},
			preRun: [][]string{
				basicNew(testName + "1"),
				basicNew(testName + "2"),
				basicNew(testName + "3"),
				{"delete", "-y", command, "--name", testName + "3"},
				basicNew(testName + "4"),
				basicNew(testName + "5"),
			},
		},
	}

	return &testSpider{projectName, tests, beforeEach, getConfigString, "library"}
}
