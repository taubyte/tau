package dream

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	spec "github.com/taubyte/tau/pkg/specs/common"
	servicesCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/utils/maps"

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
	if err := u.Provides("auth", "patrick", "tns"); err != nil {
		return err
	}

	simple, err := u.Simple("client")
	if err != nil {
		return fmt.Errorf("failed getting simple with error: %v", err)
	}

	err = simple.Provides("auth", "tns")
	if err != nil {
		return err
	}

	projectId := ""
	if len(params) > 0 {
		if p, ok := params[0].(string); ok {
			projectId = p
		}
	}

	projectRoot := ""
	if len(params) > 2 {
		if p, ok := params[2].(string); ok && p != "" {
			projectRoot = p
		}
	}

	branch := spec.DefaultBranches[0]
	if len(params) > 1 {
		if b, ok := params[1].(string); ok && b != "" {
			branch = b
		}
	}

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
		fullName, ok := _repoInfo["fullname"].(string)
		if !ok {
			continue
		}
		var err error
		if projectRoot != "" {
			// Derive per-repo local path from fullname (e.g. taubyte-test/tb_code_foo -> projectRoot/code)
			parts := strings.SplitN(fullName, "/", 2)
			name := fullName
			if len(parts) == 2 {
				name = parts[1]
			}
			var localPath string
			switch {
			case strings.HasPrefix(name, "tb_code_"):
				localPath = projectRoot + "/code"
			case strings.HasPrefix(name, "tb_website_"):
				localPath = projectRoot + "/websites/" + strings.TrimPrefix(name, "tb_website_")
			case strings.HasPrefix(name, "tb_library_"):
				localPath = projectRoot + "/libraries/" + strings.TrimPrefix(name, "tb_library_")
			default:
				localPath = projectRoot + "/config"
			}
			err = pushSpecific(u, repoId, fullName, projectId, branch, localPath)
		} else {
			err = pushSpecific(u, repoId, fullName, projectId, branch)
		}
		if err != nil {
			return err
		}
	}

	return nil
}
func pushSpecific(u *dream.Universe, params ...interface{}) error {
	if err := u.Provides("auth", "patrick", "tns"); err != nil {
		return err
	}

	simple, err := u.Simple("client")
	if err != nil {
		return fmt.Errorf("failed getting client with: %v", err)
	}

	err = simple.Provides("auth", "tns")
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
	branch := spec.DefaultBranches[0]
	if len(params) > 3 {
		if b, ok := params[3].(string); ok && b != "" {
			branch = b
		}
	}
	localPath := ""
	if len(params) > 4 {
		if p, ok := params[4].(string); ok && p != "" {
			localPath = p
		}
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

	var newPayload []byte
	if localPath != "" {
		if fi, err := os.Stat(localPath); err != nil || !fi.IsDir() {
			return fmt.Errorf("local path is not an existing directory: %s", localPath)
		}
		commitID, err := commonTest.HeadCommitFromLocalRepo(localPath)
		if err != nil {
			return fmt.Errorf("head commit from local repo: %w", err)
		}
		newPayload, err = commonTest.MakeTemplate(intRepoId, fullname, branch, commitID, "local://"+localPath)
		if err != nil {
			return fmt.Errorf("make template failed with: %v", err)
		}
	} else {
		newPayload, err = commonTest.MakeTemplate(intRepoId, fullname, branch, "", "")
		if err != nil {
			return fmt.Errorf("make template failed with: %v", err)
		}
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
	if len(params) > 0 {
		if localPath, ok := params[0].(string); ok && localPath != "" {
			if fi, err := os.Stat(localPath); err != nil || !fi.IsDir() {
				return fmt.Errorf("local path is not an existing directory: %s", localPath)
			}
			commitID, err := commonTest.HeadCommitFromLocalRepo(localPath)
			if err != nil {
				return fmt.Errorf("head commit from local repo: %w", err)
			}
			fullname := commonTest.GitUser + "/" + commonTest.ConfigRepo.Name
			payload, err := commonTest.MakeTemplate(commonTest.ConfigRepo.ID, fullname, spec.DefaultBranches[0], commitID, "local://"+localPath)
			if err != nil {
				return fmt.Errorf("make template failed with: %v", err)
			}
			return pushWrapper(u, payload, commonTest.ConfigRepo)
		}
	}
	return pushWrapper(u, commonTest.ConfigPayload, commonTest.ConfigRepo)
}

func pushCode(u *dream.Universe, params ...interface{}) error {
	if len(params) > 0 {
		if localPath, ok := params[0].(string); ok && localPath != "" {
			if fi, err := os.Stat(localPath); err != nil || !fi.IsDir() {
				return fmt.Errorf("local path is not an existing directory: %s", localPath)
			}
			commitID, err := commonTest.HeadCommitFromLocalRepo(localPath)
			if err != nil {
				return fmt.Errorf("head commit from local repo: %w", err)
			}
			fullname := commonTest.GitUser + "/" + commonTest.CodeRepo.Name
			payload, err := commonTest.MakeTemplate(commonTest.CodeRepo.ID, fullname, spec.DefaultBranches[0], commitID, "local://"+localPath)
			if err != nil {
				return fmt.Errorf("make template failed with: %v", err)
			}
			return pushWrapper(u, payload, commonTest.CodeRepo)
		}
	}
	return pushWrapper(u, commonTest.CodePayload, commonTest.CodeRepo)
}

func pushWebsite(u *dream.Universe, params ...interface{}) error {
	simple, err := u.Simple("client")
	if err != nil {
		return fmt.Errorf("unable to get simple client: %w", err)
	}

	auth, err := simple.Auth()
	if err != nil {
		return err
	}

	commonTest.RegisterTestRepositories(u.Context(), auth, commonTest.WebsiteRepo)

	if len(params) > 0 {
		if localPath, ok := params[0].(string); ok && localPath != "" {
			if fi, err := os.Stat(localPath); err != nil || !fi.IsDir() {
				return fmt.Errorf("local path is not an existing directory: %s", localPath)
			}
			commitID, err := commonTest.HeadCommitFromLocalRepo(localPath)
			if err != nil {
				return fmt.Errorf("head commit from local repo: %w", err)
			}
			fullname := commonTest.GitUser + "/" + commonTest.WebsiteRepo.Name
			payload, err := commonTest.MakeTemplate(commonTest.WebsiteRepo.ID, fullname, spec.DefaultBranches[0], commitID, "local://"+localPath)
			if err != nil {
				return fmt.Errorf("make template failed with: %v", err)
			}
			return pushWrapper(u, payload, commonTest.WebsiteRepo)
		}
	}
	return pushWrapper(u, commonTest.WebsitePayload, commonTest.WebsiteRepo)
}

func pushLibrary(u *dream.Universe, params ...interface{}) error {
	simple, err := u.Simple("client")
	if err != nil {
		return fmt.Errorf("unable to get simple client: %w", err)
	}

	auth, err := simple.Auth()
	if err != nil {
		return fmt.Errorf("unable to get auth: %w", err)
	}

	commonTest.RegisterTestRepositories(u.Context(), auth, commonTest.LibraryRepo)

	if len(params) > 0 {
		if localPath, ok := params[0].(string); ok && localPath != "" {
			if fi, err := os.Stat(localPath); err != nil || !fi.IsDir() {
				return fmt.Errorf("local path is not an existing directory: %s", localPath)
			}
			commitID, err := commonTest.HeadCommitFromLocalRepo(localPath)
			if err != nil {
				return fmt.Errorf("head commit from local repo: %w", err)
			}
			fullname := commonTest.GitUser + "/" + commonTest.LibraryRepo.Name
			payload, err := commonTest.MakeTemplate(commonTest.LibraryRepo.ID, fullname, spec.DefaultBranches[0], commitID, "local://"+localPath)
			if err != nil {
				return fmt.Errorf("make template failed with: %v", err)
			}
			return pushWrapper(u, payload, commonTest.LibraryRepo)
		}
	}
	return pushWrapper(u, commonTest.LibraryPayload, commonTest.LibraryRepo)
}

func pushWrapper(u *dream.Universe, gitPayload []byte, repo commonTest.Repository) error {
	if err := u.Provides("auth", "patrick", "tns"); err != nil {
		return fmt.Errorf("unable to provide: %w", err)
	}

	mockPatrickURL, err := u.GetURLHttp(u.Patrick().Node())
	if err != nil {
		return fmt.Errorf("unable to get url http: %w", err)
	}

	servicesCommon.FakeSecret = true
	fmt.Printf("Pushing job to from repo %s. ProjectID: %s\n", repo.Name, commonTest.ProjectID)

	if err := commonTest.PushJob(gitPayload, mockPatrickURL, repo); err != nil {
		return fmt.Errorf("unable to push job: %w", err)
	}

	return nil
}
