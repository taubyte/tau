package service

import (
	_ "embed"
	"fmt"
	"time"

	commonTest "github.com/taubyte/tau/dream/helpers"

	"github.com/taubyte/tau/core/services/tns"
	spec "github.com/taubyte/tau/pkg/specs/common"
	servicesCommon "github.com/taubyte/tau/services/common"

	"github.com/taubyte/tau/dream"

	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/monkey/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
)

func init() {
	dream.RegisterFixture("createProjectWithJobs", createProjectWithJobs)
}

func createProjectWithJobs(u *dream.Universe, params ...interface{}) error {
	simple, err := u.Simple("client")
	if err != nil {
		return err
	}

	err = simple.Provides("tns")
	if err != nil {
		return err
	}

	err = u.Provides(
		"auth",
		"patrick",
		"monkey",
		"hoarder",
		"tns",
	)
	if err != nil {
		return err
	}

	auth, err := simple.Auth()
	if err != nil {
		return err
	}

	attempts := 0
	var tnsClient tns.Client
	// TODO: Why 50 attempts, might be better to sleep
	for tnsClient == nil {
		if attempts == 50 {
			return fmt.Errorf("unable to get tns client after 50 attempts")
		}

		tnsClient, _ = simple.TNS()
		attempts++
	}

	mockPatrickURL, err := u.GetURLHttp(u.Patrick().Node())
	if err != nil {
		return err
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
		return err
	}

	servicesCommon.FakeSecret = true
	if err = commonTest.PushJob(commonTest.ConfigPayload, mockPatrickURL, commonTest.ConfigRepo); err != nil {
		return fmt.Errorf("pushing conifg job failed with %w", err)
	}

	time.Sleep(3 * time.Second)

	if err = commonTest.PushJob(commonTest.CodePayload, mockPatrickURL, commonTest.CodeRepo); err != nil {
		return fmt.Errorf("pushing code job failed with %w", err)
	}

	attempts = 0
	for {
		commitObj, err := tnsClient.Fetch(spec.Current(commonTest.ProjectID, spec.DefaultBranches[1]))
		if err == nil {
			if _, ok := commitObj.Interface().(string); ok {
				break
			}
		}

		attempts++
		if attempts > 50 {
			return fmt.Errorf("failed fetching from tns after %d attempts", attempts)
		}

		time.Sleep(3 * time.Second)
	}

	return nil
}
