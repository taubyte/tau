package service

import (
	_ "embed"
	"fmt"
	"time"

	commonTest "github.com/taubyte/tau/dream/helpers"

	"github.com/taubyte/tau/core/services/tns"
	spec "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
	servicesCommon "github.com/taubyte/tau/services/common"

	"github.com/taubyte/tau/dream"

	_ "github.com/taubyte/tau/services/auth"
	_ "github.com/taubyte/tau/services/hoarder"
	_ "github.com/taubyte/tau/services/monkey"
	_ "github.com/taubyte/tau/services/tns"
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

	mockAuthURL, err := u.GetURLHttp(u.Auth().Node())
	if err != nil {
		return err
	}

	mockPatrickURL, err := u.GetURLHttp(u.Patrick().Node())
	if err != nil {
		return err
	}

	// override ID of project generated so that it matches id in config
	servicesCommon.GetNewProjectID = func(args ...interface{}) string { return commonTest.ProjectID }

	if err = commonTest.RegisterTestProject(u.Context(), mockAuthURL); err != nil {
		return fmt.Errorf("registering test project failed with %w", err)
	}

	servicesCommon.FakeSecret = true
	if err = commonTest.PushJob(commonTest.ConfigPayload, mockPatrickURL, commonTest.ConfigRepo); err != nil {
		return fmt.Errorf("pushing conifg job failed with %w", err)
	}

	time.Sleep(3 * time.Second)

	if err = commonTest.PushJob(commonTest.CodePayload, mockPatrickURL, commonTest.CodeRepo); err != nil {
		return fmt.Errorf("pushing code job failed with %w", err)
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

	attempts = 0
	var response tns.Object
	response = newEmptyObject()
	for {
		commitObj, err := tnsClient.Fetch(spec.Current(commonTest.ProjectID, spec.DefaultBranches[0]))
		if err != nil {
			fmt.Printf("Getting current commit failed with: %s\n", err)
		} else {
			commit, ok := commitObj.Interface().(string)
			if !ok {
				fmt.Printf("Cannot convert commit interface{} `%v` to string\n", commitObj.Interface())
			} else {
				response, err = tnsClient.Fetch(methods.ProjectPrefix(commonTest.ProjectID, spec.DefaultBranches[0], commit))
				if err != nil {
					fmt.Printf("Fetching project from prefix failed with: %v\n", err)
				}
			}
		}

		if response.Interface() != nil {
			fmt.Println("Response from TNS", response)
			break
		}
		attempts += 1
		if attempts == 50 {
			return fmt.Errorf("failed fetching from tns after %d attempts", attempts)
		}

		time.Sleep(1 * time.Second)
	}

	if err = commonTest.RegisterTestDomain(u.Context(), mockAuthURL); err != nil {
		return err
	}

	return nil
}
