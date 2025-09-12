package dream

import (
	_ "embed"
	"fmt"
	"time"

	commonTest "github.com/taubyte/tau/dream/helpers"
	"github.com/taubyte/tau/utils/maps"

	"github.com/taubyte/tau/core/services/tns"
	spec "github.com/taubyte/tau/pkg/specs/common"
	servicesCommon "github.com/taubyte/tau/services/common"

	"github.com/taubyte/tau/dream"

	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
)

func init() {
	dream.RegisterFixture("createProjectWithJobs", createProjectWithJobs)
}

// waitForTNSObjects waits for multiple TNS objects to be available with the specified IDs
func waitForTNSObjects(tnsClient tns.Client, repoIDs []int, maxAttempts int, retryDelay time.Duration) error {
	if len(repoIDs) == 0 {
		return nil
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		allFound := true
		var lastErr error

		for _, repoID := range repoIDs {
			expectedID := fmt.Sprintf("%d", repoID)
			tnsPath := spec.NewTnsPath([]string{"resolve", "repo", "github", expectedID})

			obj, err := tnsClient.Fetch(tnsPath)
			if err != nil {
				allFound = false
				lastErr = err
				break
			}

			// Check if the object contains the expected ID
			if objMap, ok := obj.Interface().(map[interface{}]interface{}); ok {
				id, _ := maps.String(maps.SafeInterfaceToStringKeys(objMap), "id")
				sshUrl, _ := maps.String(maps.SafeInterfaceToStringKeys(objMap), "ssh")
				if id == expectedID && sshUrl != "" {
					continue // This repo is found, check the next one
				}
			}

			allFound = false
			break
		}

		if allFound {
			return nil
		}

		if attempt == maxAttempts {
			if lastErr != nil {
				return fmt.Errorf("failed to fetch from TNS after %d attempts: %w", maxAttempts, lastErr)
			}
			return fmt.Errorf("not all repositories found in TNS after %d attempts", maxAttempts)
		}

		time.Sleep(retryDelay)
	}

	return fmt.Errorf("unexpected error: exceeded max attempts without returning")
}

func createProjectWithJobs(u *dream.Universe, params ...interface{}) error {
	if err := u.Provides("auth", "patrick", "tns"); err != nil {
		return err
	}

	simple, err := u.Simple("client")
	if err != nil {
		return fmt.Errorf("unable to get simple client: %w", err)
	}

	if err := simple.Provides("tns"); err != nil {
		return fmt.Errorf("unable to get tns: %w", err)
	}

	auth, err := simple.Auth()
	if err != nil {
		return fmt.Errorf("unable to get auth: %w", err)
	}

	attempts := 0
	var tnsClient tns.Client
	// TODO: Why 3 attempts, might be better to sleep
	for tnsClient == nil {
		if attempts == 3 {
			return fmt.Errorf("unable to get tns client after 3 attempts")
		}

		tnsClient, _ = simple.TNS()
		attempts++
		time.Sleep(1 * time.Second)
	}

	mockPatrickURL, err := u.GetURLHttp(u.Patrick().Node())
	if err != nil {
		return fmt.Errorf("unable to get url http: %w", err)
	}

	// override ID of project generated so that it matches id in config
	servicesCommon.GetNewProjectID = func(args ...interface{}) string { return commonTest.ProjectID }

	if err = commonTest.RegisterTestProject(u.Context(), auth); err != nil {
		return fmt.Errorf("registering test project failed with %w", err)
	}

	if err = commonTest.RegisterTestRepositories(u.Context(), auth, commonTest.ConfigRepo, commonTest.CodeRepo); err != nil {
		return fmt.Errorf("registering test repositories failed with %w", err)
	}

	if err = commonTest.RegisterTestDomain(u.Context(), auth); err != nil {
		return fmt.Errorf("registering test domain failed with %w", err)
	}

	servicesCommon.FakeSecret = true
	if err = commonTest.PushJob(commonTest.ConfigPayload, mockPatrickURL, commonTest.ConfigRepo); err != nil {
		return fmt.Errorf("pushing conifg job failed with %w", err)
	}

	time.Sleep(3 * time.Second)

	if err = commonTest.PushJob(commonTest.CodePayload, mockPatrickURL, commonTest.CodeRepo); err != nil {
		return fmt.Errorf("pushing code job failed with %w", err)
	}

	// Wait for both repositories to be available in TNS
	if err := waitForTNSObjects(tnsClient, []int{commonTest.ConfigRepo.ID, commonTest.CodeRepo.ID}, 20, 500*time.Millisecond); err != nil {
		return fmt.Errorf("waiting for repositories in TNS failed: %w", err)
	}

	return nil
}
