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

// Retry function with a custom condition
func Retry(maxRetries int, waitBeforeRetry time.Duration, operation interface{}, condition interface{}, args ...interface{}) ([]interface{}, error) {
	opVal := reflect.ValueOf(operation)
	condVal := reflect.ValueOf(condition)

	if opVal.Kind() != reflect.Func {
		return nil, fmt.Errorf("operation must be a function")
	}

	if condVal.Kind() != reflect.Func {
		return nil, fmt.Errorf("condition must be a function")
	}

	for i := 0; i < maxRetries; i++ {
		in := make([]reflect.Value, len(args))
		for j, arg := range args {
			in[j] = reflect.ValueOf(arg)
		}

		// Call the operation function
		out := opVal.Call(in)
		outSlice := make([]interface{}, len(out))
		for i, v := range out {
			outSlice[i] = v.Interface()
		}

		// Apply the condition function
		condIn := make([]reflect.Value, len(outSlice))
		for j, arg := range outSlice {
			condIn[j] = reflect.ValueOf(arg)
		}

		condResult := condVal.Call(condIn)
		shouldRetry := condResult[0].Bool()

		if !shouldRetry {
			return outSlice, nil
		}

		time.Sleep(waitBeforeRetry)
	}
	return nil, errors.New("operation failed after max retries")
}

func (m *Monkey) getGithubDeploymentKeyWithRetry(maxRetries int, waitBeforeRetry time.Duration, gitRepo *auth.GithubRepository, ac auth.Client, repoID int) (string, error) {
	operation := func(gitRepo *auth.GithubRepository, ac auth.Client, repoID int) (string, error) {
		deployKey := (*gitRepo).PrivateKey()
		if len(deployKey) != 0 {
			return deployKey, nil
		}
		fmt.Println("Deploy key is empty, retrying...")
		updatedRepo, err := ac.Repositories().Github().Get(repoID)
		if err != nil {
			return "", err
		}
		*gitRepo = updatedRepo
		return updatedRepo.PrivateKey(), nil
	}

	condition := func(deployKey string, err error) bool {
		if deployKey == "" {
			return true // Retry if the result is an empty string
		}
		if err != nil {
			return false // Don't retry if there is an error
		}
		return false // Do not retry if there is no error and result is non-empty
	}

	result, err := Retry(maxRetries, waitBeforeRetry, operation, condition, gitRepo, ac, repoID)
	if err != nil {
		return "", err
	}
	return result[0].(string), nil
}
