package service

import (
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	commonTest "github.com/taubyte/tau/dream/helpers"
	spec "github.com/taubyte/tau/pkg/specs/common"
	servicesCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/utils/maps"

	"github.com/taubyte/tau/dream"

	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/monkey/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
)

func init() {
	dream.RegisterFixture("pushConfig", pushConfig)
	dream.RegisterFixture("pushCode", pushCode)
	dream.RegisterFixture("pushWebsite", pushWebsite)
	dream.RegisterFixture("pushLibrary", pushLibrary)
	dream.RegisterFixture("pushSpecific", pushSpecific)
	dream.RegisterFixture("pushAll", pushAll)
}

func pushAll(u *dream.Universe, params ...interface{}) error {
	simple, err := u.Simple("client")
	if err != nil {
		return fmt.Errorf("failed getting simple with error: %v", err)
	}

	err = simple.Provides("auth", "tns")
	if err != nil {
		return err
	}

	err = u.Provides(
		"auth",
		"patrick",
		"monkey",
		"tns",
	)
	if err != nil {
		return err
	}

	projectId := ""
	tns, err := simple.TNS()
	if err != nil {
		return err
	}

	resp, err := tns.Fetch(spec.NewTnsPath([]string{"resolve", "repo", "github"}))
	if err != nil {
		return fmt.Errorf("failed calling tns fetch with error: %v", err)
	}

	_map := maps.SafeInterfaceToStringKeys(resp.Interface())

	for repoId, repoInfo := range _map {
		_repoInfo := maps.SafeInterfaceToStringKeys(repoInfo)
		fullName, ok := _repoInfo["fullname"]
		if !ok {
			return fmt.Errorf("fullname does not exist for repo : %s", repoId)
		}

		err := pushSpecific(u, repoId, fullName, projectId, spec.DefaultBranches[0]) // TODO: add param to provide branch
		if err != nil {
			return err
		}
	}

	return nil
}
func pushSpecific(u *dream.Universe, params ...interface{}) error {
	simple, err := u.Simple("client")
	if err != nil {
		return fmt.Errorf("failed getting client with: %v", err)
	}

	err = simple.Provides("auth", "tns")
	if err != nil {
		return err
	}

	err = u.Provides(
		"auth",
		"patrick",
		"monkey",
		"tns",
	)
	if err != nil {
		return err
	}

	if len(params) < 2 {
		return errors.New("pushSpecific expects two parameters [repository-Id] [repository-fullname] ")
	}
	repoId := params[0].(string)
	fullname := params[1].(string)
	projectId := ""
	if len(params) > 2 {
		projectId = params[2].(string)
	}

	intRepoId, err := strconv.Atoi(repoId)
	if err != nil {
		return fmt.Errorf("failed getting repo ID: %v", err)
	}

	auth, err := simple.Auth()
	if err != nil {
		return err
	}

	// Try to register
	commonTest.RegisterTestRepositories(u.Context(), auth, commonTest.Repository{
		ID:   intRepoId,
		Name: strings.Split(fullname, "/")[1],
	})
	time.Sleep(1 * time.Second)

	newPayload, err := commonTest.MakeTemplate(intRepoId, fullname, spec.DefaultBranches[0]) // TODO: add param to provide branch
	if err != nil {
		return fmt.Errorf("make template failed with: %v", err)
	}

	// Try to get projectId from repo
	if len(projectId) == 0 {
		_repo, err := auth.Repositories().Github().Get(intRepoId)
		if err != nil {
			return fmt.Errorf("failed to fetch Repo: %v", err)
		}
		projectId = _repo.Project()
	}

	if len(projectId) != 0 {
		tempId := commonTest.ProjectID
		commonTest.ProjectID = projectId

		// Reset the projectId after the push
		defer func() {
			commonTest.ProjectID = tempId
		}()
	}

	return pushWrapper(u, newPayload, commonTest.Repository{ID: intRepoId})
}

func pushConfig(u *dream.Universe, params ...interface{}) error {
	return pushWrapper(u, commonTest.ConfigPayload, commonTest.ConfigRepo)
}

func pushCode(u *dream.Universe, params ...interface{}) error {
	return pushWrapper(u, commonTest.CodePayload, commonTest.CodeRepo)
}

func pushWebsite(u *dream.Universe, params ...interface{}) error {
	// Create a simple to get the auth client
	simple, err := u.Simple("client")
	if err != nil {
		// If simple doesn't exist, create it
		err = u.StartWithConfig(&dream.Config{
			Simples: map[string]dream.SimpleConfig{
				"client": {
					Clients: dream.SimpleConfigClients{
						Auth: &commonIface.ClientConfig{},
					}.Compat(),
				},
			},
		})
		if err != nil {
			return err
		}
		simple, err = u.Simple("client")
		if err != nil {
			return err
		}
	}

	auth, err := simple.Auth()
	if err != nil {
		return err
	}

	// Try to register
	commonTest.RegisterTestRepositories(u.Context(), auth, commonTest.WebsiteRepo)

	err = pushWrapper(u, commonTest.WebsitePayload, commonTest.WebsiteRepo)
	if err != nil {
		return err
	}

	return nil
}

func pushLibrary(u *dream.Universe, params ...interface{}) error {
	// Create a simple to get the auth client
	simple, err := u.Simple("client")
	if err != nil {
		// If simple doesn't exist, create it
		err = u.StartWithConfig(&dream.Config{
			Simples: map[string]dream.SimpleConfig{
				"client": {
					Clients: dream.SimpleConfigClients{
						Auth: &commonIface.ClientConfig{},
					}.Compat(),
				},
			},
		})
		if err != nil {
			return err
		}
		simple, err = u.Simple("client")
		if err != nil {
			return err
		}
	}

	auth, err := simple.Auth()
	if err != nil {
		return err
	}

	// Try to register
	commonTest.RegisterTestRepositories(u.Context(), auth, commonTest.LibraryRepo)

	err = pushWrapper(u, commonTest.LibraryPayload, commonTest.LibraryRepo)
	if err != nil {
		return err
	}

	return nil
}

func pushWrapper(u *dream.Universe, gitPayload []byte, repo commonTest.Repository) error {
	err := u.Provides(
		"auth",
		"patrick",
		"monkey",
		"hoarder",
		"tns",
	)
	if err != nil {
		return err
	}

	mockPatrickURL, err := u.GetURLHttp(u.Patrick().Node())
	if err != nil {
		return err
	}

	servicesCommon.FakeSecret = true
	fmt.Printf("Pushing job to from repo %s. ProjectID: %s\n", repo.Name, commonTest.ProjectID)
	if err := commonTest.PushJob(gitPayload, mockPatrickURL, repo); err != nil {
		return err
	}

	return nil
}
