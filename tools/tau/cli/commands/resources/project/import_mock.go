//go:build mockGithub
// +build mockGithub

package project

import (
	"context"
	"fmt"

	"github.com/google/go-github/v71/github"
	commonTest "github.com/taubyte/tau/tools/tau/common/test"
)

// TODO: If tests are run using go run . rather than building cli, then this can be avoided
var (
	configId           int64 = 1234
	codeId             int64 = 5678
	configRepoName           = "tb_Repo"
	configRepoFullName       = commonTest.GitUser + "/" + configRepoName
	codeRepoName             = "tb_code_Repo"
	codeRepoFullName         = commonTest.GitUser + "/" + codeRepoName

	mockConfigRepo = &github.Repository{
		ID:       &configId,
		Name:     &configRepoName,
		FullName: &configRepoFullName,
	}

	mockCodeRepo = &github.Repository{
		ID:       &codeId,
		Name:     &codeRepoName,
		FullName: &codeRepoFullName,
	}
)

// used for tests
func init() {
	GetRepo = func(ctx context.Context, token, fullName string) (*github.Repository, error) {
		for _, r := range []*github.Repository{mockConfigRepo, mockCodeRepo} {
			if r.GetFullName() == fullName {
				return r, nil
			}
		}
		return nil, fmt.Errorf("no repository %s", fullName)
	}
	ConfigProjectID = func(ctx context.Context, token, fullName string) (string, error) {
		return "", nil
	}
	ListRepos = func(ctx context.Context, token, user string) ([]*github.Repository, error) {
		return []*github.Repository{
			mockConfigRepo,
			mockCodeRepo,
		}, nil
	}
}
