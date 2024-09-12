package monkey

import (
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/taubyte/tau/core/services/auth"
)

func toNumber(in interface{}) int {
	i := reflect.ValueOf(in)
	switch i.Kind() {
	case reflect.Int64:
		return int(i.Int())
	case reflect.Uint64:
		return int(i.Uint())
	}
	return 0
}

func (m *Monkey) appendErrors(r io.WriteSeeker, errors chan error) {
	if len(errors) > 0 {
		r.Seek(0, io.SeekEnd)
		r.Write([]byte("\nCI/CD Errors:\n\n"))
		for err := range errors {
			r.Write([]byte(err.Error() + "\n"))
		}
	}
}

func (m *Monkey) storeLogs(r io.ReadSeeker) (string, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("logs seek start failed with: %w", err)
	}

	cid, err := m.Service.node.AddFile(r)
	if err != nil {
		return "", fmt.Errorf("adding logs to node failed with: %w", err)
	}

	return cid, nil
}

var (
	GetGitRepoMaxRetries      = 3
	GetGitRepoWaitBeforeRetry = 5 * time.Second
)

func (m *Monkey) tryGetGitRepo(
	ac auth.Client,
	repoID int,
) (gitRepo auth.GithubRepository, err error) {
	for i := 0; i < GetGitRepoMaxRetries; i++ {
		gitRepo, err = ac.Repositories().Github().Get(repoID)
		if err != nil {
			return gitRepo, fmt.Errorf("fetching repository %d from auth failed with %w", repoID, err)
		}

		deployKey := gitRepo.PrivateKey()
		if len(deployKey) != 0 {
			break
		}

		if i < GetGitRepoMaxRetries-1 {
			time.Sleep(GetGitRepoWaitBeforeRetry)
		}
	}

	return gitRepo, nil
}
