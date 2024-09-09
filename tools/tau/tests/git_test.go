package tests

import (
	"fmt"
	"testing"

	commonTest "github.com/taubyte/tau/tools/tau/common/test"
	"github.com/taubyte/tau/tools/tau/constants"
	"github.com/taubyte/tau/tools/tau/singletons/session"
)

func TestGitAll(t *testing.T) {
	t.Skip("authNodeUrl - use dream instead")
	runTests(t, createGitMonkey(t), true)
}

func createGitMonkey(t *testing.T) *testSpider {
	userName := commonTest.GitUser
	token := commonTest.GitToken(t)
	projectName := commonTest.ProjectName
	branch := commonTest.Branch
	provider := "github"
	network := "Test"

	// Run before each test
	beforeEach := func(tt testMonkey) [][]string {
		tt.env[constants.CurrentSelectedNetworkName] = network
		return nil
	}

	tests := []testMonkey{
		{
			name: "basic new",
			args: []string{
				"clone", "project",
				"--no-embed-token",
				"--branch", branch,

				"--color", "never",
			},
			wantOut: []string{
				fmt.Sprintf("Cloning %s complete", commonTest.ConfigRepo.URL),
				fmt.Sprintf("Cloning %s complete", commonTest.CodeRepo.URL),
			},
			wantDir: []string{
				fmt.Sprintf("%s/config", projectName),
				fmt.Sprintf("%s/code", projectName),
			},
			preRun: [][]string{
				{"login", "--name", userName, "--provider", provider, "--token", token},
				{"select", "project", projectName},
			},
			evaluateSession: func(g session.Getter) error {
				err := expectSelectedProject(projectName)(g)
				if err != nil {
					return err
				}

				err = expectProfileName(userName)(g)
				if err != nil {
					return err
				}

				return nil
			},
		},
	}
	return &testSpider{"test_project", tests, beforeEach, nil, "git"}
}
