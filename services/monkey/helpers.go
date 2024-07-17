package monkey

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/taubyte/tau/core/services/auth"
)

func ToNumber(in interface{}) int {
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

func (m *Monkey) getGithubDeploymentKeyWithRetry(
	maxRetries int,
	waitBeforeRetry time.Duration,
	gitRepo auth.GithubRepository,
	ac auth.Client,
	repoID int,
) (deployKey string, err error) {
	for i := 0; i < maxRetries; i++ {
		deployKey = gitRepo.PrivateKey()
		if len(deployKey) != 0 {
			return
		}

		logger.Debug("Deploy key is empty, retrying")
		time.Sleep(waitBeforeRetry)
		gitRepo, err = ac.Repositories().Github().Get(repoID)
		if err != nil {
			return deployKey, fmt.Errorf("auth github get failed with: %w", err)
		}
	}
	return deployKey, errors.New("getting deploy key failed")
}
