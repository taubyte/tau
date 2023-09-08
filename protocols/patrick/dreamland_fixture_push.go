package service

import (
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	spec "github.com/taubyte/go-specs/common"
	"github.com/taubyte/tau/libdream"
	commonTest "github.com/taubyte/tau/libdream/helpers"
	protocolsCommon "github.com/taubyte/tau/protocols/common"
	"github.com/taubyte/utils/maps"

	_ "github.com/taubyte/tau/protocols/auth"
	_ "github.com/taubyte/tau/protocols/hoarder"
	_ "github.com/taubyte/tau/protocols/monkey"
	_ "github.com/taubyte/tau/protocols/tns"
)

func init() {
	libdream.RegisterFixture("pushConfig", pushConfig)
	libdream.RegisterFixture("pushCode", pushCode)
	libdream.RegisterFixture("pushWebsite", pushWebsite)
	libdream.RegisterFixture("pushLibrary", pushLibrary)
	libdream.RegisterFixture("pushSpecific", pushSpecific)
	libdream.RegisterFixture("pushAll", pushAll)
}

func pushAll(u *libdream.Universe, params ...interface{}) error {
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

	resp, err := simple.TNS().Fetch(spec.NewTnsPath([]string{"resolve", "repo", "github"}))
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

		err := pushSpecific(u, repoId, fullName, projectId, spec.DefaultBranch)
		if err != nil {
			return err
		}
	}

	return nil
}
func pushSpecific(u *libdream.Universe, params ...interface{}) error {
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

	mockAuthURL, err := u.GetURLHttp(u.Auth().Node())
	if err != nil {
		return err
	}

	// Try to register
	commonTest.RegisterTestRepositories(u.Context(), mockAuthURL, commonTest.Repository{
		ID:   intRepoId,
		Name: strings.Split(fullname, "/")[1],
	})
	time.Sleep(1 * time.Second)

	newPayload, err := commonTest.MakeTemplate(intRepoId, fullname, spec.DefaultBranch)
	if err != nil {
		return fmt.Errorf("make template failed with: %v", err)
	}

	// Try to get projectId from repo
	if len(projectId) == 0 {
		_repo, err := simple.Auth().Repositories().Github().Get(intRepoId)
		if err != nil {
			return fmt.Errorf("failed Making Repo: %v", err)
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

func pushConfig(u *libdream.Universe, params ...interface{}) error {
	return pushWrapper(u, commonTest.ConfigPayload, commonTest.ConfigRepo)
}

func pushCode(u *libdream.Universe, params ...interface{}) error {
	return pushWrapper(u, commonTest.CodePayload, commonTest.CodeRepo)
}

func pushWebsite(u *libdream.Universe, params ...interface{}) error {

	mockAuthURL, err := u.GetURLHttp(u.Auth().Node())
	if err != nil {
		return err
	}

	// Try to register
	commonTest.RegisterTestRepositories(u.Context(), mockAuthURL, commonTest.WebsiteRepo)

	err = pushWrapper(u, commonTest.WebsitePayload, commonTest.WebsiteRepo)
	if err != nil {
		return err
	}

	return nil
}

func pushLibrary(u *libdream.Universe, params ...interface{}) error {

	mockAuthURL, err := u.GetURLHttp(u.Auth().Node())
	if err != nil {
		return err
	}

	// Try to register
	commonTest.RegisterTestRepositories(u.Context(), mockAuthURL, commonTest.LibraryRepo)

	err = pushWrapper(u, commonTest.LibraryPayload, commonTest.LibraryRepo)
	if err != nil {
		return err
	}

	return nil
}

func pushWrapper(u *libdream.Universe, gitPayload []byte, repo commonTest.Repository) error {
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

	protocolsCommon.FakeSecret = true
	fmt.Printf("Pushing job to repo %v  projectID: %s\n", repo, commonTest.ProjectID)
	err = commonTest.PushJob(gitPayload, mockPatrickURL, repo)
	if err != nil {
		return err
	}

	return nil
}
