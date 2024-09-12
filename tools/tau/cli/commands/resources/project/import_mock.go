//go:build mockGithub
// +build mockGithub

package project

import (
	"context"

	"github.com/google/go-github/v53/github"
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
	ListRepos = func(ctx context.Context, token, user string) ([]*github.Repository, error) {
		return []*github.Repository{
			mockConfigRepo,
			mockCodeRepo,
		}, nil
	}
}
