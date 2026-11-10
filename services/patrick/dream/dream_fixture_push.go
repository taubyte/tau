package dream

import (
	"cmp"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"

	authIface "github.com/taubyte/tau/core/services/auth"
	patrickIface "github.com/taubyte/tau/core/services/patrick"
	tnsIface "github.com/taubyte/tau/core/services/tns"
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

	force := false
	if len(params) > 3 {
		if f, ok := params[3].(string); ok {
			force, _ = strconv.ParseBool(f)
		}
	}

	tns, err := simple.TNS()
	if err != nil {
		return err
	}

	auth, err := simple.Auth()
	if err != nil {
		return err
	}

	// Each repo's newest job, to skip repos whose head already built. Without
	// Patrick nothing is skipped.
	var jobs map[int]*patrickIface.Job
	var patrick patrickIface.Client
	if err := simple.Provides("patrick"); err == nil {
		if p, err := simple.Patrick(); err == nil {
			patrick = p
			if !force {
				jobs = newestJobs(p)
			}
		}
	}

	type repo struct{ id, fullName string }
	list := func() ([]repo, error) {
		resp, err := tns.Fetch(spec.NewTnsPath([]string{"resolve", "repo", "github"}))
		if err != nil {
			return nil, fmt.Errorf("failed calling tns fetch with error: %v", err)
		}
		var repos []repo
		for repoId, repoInfo := range maps.SafeInterfaceToStringKeys(resp.Interface()) {
			fullName, ok := maps.SafeInterfaceToStringKeys(repoInfo)["fullname"].(string)
			if !ok {
				continue
			}
			// The universe's repos are every project's: with a project id, push
			// only that project's, rather than rebuilding every project there is.
			if projectId != "" {
				if owner := repoProject(auth, tns, repoId); owner != "" && owner != projectId {
					continue
				}
			}
			repos = append(repos, repo{repoId, fullName})
		}
		// Config first: the other repos' jobs read what its job publishes.
		slices.SortStableFunc(repos, func(a, b repo) int {
			return cmp.Compare(repoKindOrder(a.fullName), repoKindOrder(b.fullName))
		})
		return repos, nil
	}

	repos, err := list()
	if err != nil {
		return err
	}

	// The list above holds the repositories auth has registered; a re-imported
	// project has only its config and code there. Its websites and libraries
	// are named in its compiled config: note the config's commit now, and after
	// the first pass push those (waiting for the config job if the project has
	// no commit yet). pushSpecific registers each one it pushes.
	configProject, commitBefore, configRepo := "", "", 0
	for _, r := range repos {
		if repoKindOrder(r.fullName) == 0 {
			if configProject = projectId; configProject == "" {
				configProject = repoProject(auth, tns, r.id)
			}
			commitBefore, _, _ = tns.Simple().Commit(configProject, branch)
			configRepo, _ = strconv.Atoi(r.id)
			break
		}
	}

	pushed := make(map[string]bool)
	startedAt, configSent := time.Now().Unix(), false
	pushIdx := 0
	push := func(r repo) error {
		repoId, fullName := r.id, r.fullName
		pushed[repoId] = true
		pushedAt := time.Now().Unix() + int64(pushIdx)
		pushIdx++
		localPath := ""
		if projectRoot != "" {
			// Derive per-repo local path from fullname (e.g. taubyte-test/tb_code_foo -> projectRoot/code)
			parts := strings.SplitN(fullName, "/", 2)
			name := fullName
			if len(parts) == 2 {
				name = parts[1]
			}
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
			// Without a local path the payload carries the default commit: always rebuilt.
			if commit, err := commonTest.HeadCommitFromLocalRepo(localPath); err == nil && built(jobs, repoId, commit) {
				fmt.Printf("Skipping %s: commit %s already built\n", fullName, commit)
				return nil
			}
		}
		configSent = configSent || repoKindOrder(fullName) == 0
		return pushSpecific(u, repoId, fullName, projectId, branch, localPath, pushedAt)
	}

	for _, r := range repos {
		if err := push(r); err != nil {
			return err
		}
	}

	if configProject == "" {
		return nil
	}
	// The websites and libraries are read from the config the config job
	// publishes: wait for that job, or a website just added to the config of an
	// already-built project is read from the old config and never pushed.
	if patrick != nil && configSent && configRepo != 0 {
		waitForJob(u.Context(), patrick, configRepo, startedAt, newCommitWait)
	} else if commitBefore == "" {
		waitForNewCommit(u.Context(), tns.Simple(), configProject, branch, commitBefore, newCommitWait)
	}
	websites, _, _, _ := tns.Website().All(configProject, "", branch).List()
	libraries, _, _, _ := tns.Library().All(configProject, "", branch).List()
	for _, r := range configuredRepos(websites, libraries) {
		if pushed[r.id] {
			continue
		}
		if err := push(repo(r)); err != nil {
			return err
		}
	}

	return nil
}

// newestJobs is each repository's newest job in Patrick.
func newestJobs(p patrickIface.Client) map[int]*patrickIface.Job {
	ids, err := p.List()
	if err != nil {
		return nil
	}
	jobs := make(map[int]*patrickIface.Job)
	for _, id := range ids {
		j, err := p.Get(id)
		if err != nil {
			continue
		}
		repo := j.Meta.Repository.ID
		if old, ok := jobs[repo]; !ok || j.Timestamp > old.Timestamp {
			jobs[repo] = j
		}
	}
	return jobs
}

// built is true when repoId's newest job built commit successfully.
// The default commit is what payloads carry with no local repo: never built.
func built(jobs map[int]*patrickIface.Job, repoId, commit string) bool {
	id, err := strconv.Atoi(repoId)
	if err != nil || commit == "" || commit == commonTest.DefaultCommitID {
		return false
	}
	j := jobs[id]
	return j != nil && j.Status == patrickIface.JobStatusSuccess && j.Meta.HeadCommit.ID == commit
}

type configuredRepo struct{ id, fullName string }

// configuredRepos lists the repositories a project's config names for its
// websites and libraries: libraries first, as their jobs come before.
func configuredRepos(websites map[string]*structureSpec.Website, libraries map[string]*structureSpec.Library) []configuredRepo {
	var repos []configuredRepo
	seen := make(map[string]bool)
	add := func(id, name string) {
		if id != "" && name != "" && !seen[id] {
			seen[id] = true
			repos = append(repos, configuredRepo{id, name})
		}
	}
	for _, l := range libraries {
		add(l.RepoID, l.RepoName)
	}
	for _, w := range websites {
		add(w.RepoID, w.RepoName)
	}
	slices.SortStableFunc(repos, func(a, b configuredRepo) int {
		return cmp.Or(cmp.Compare(repoKindOrder(a.fullName), repoKindOrder(b.fullName)), cmp.Compare(a.id, b.id))
	})
	return repos
}

// newCommitWait is how long push-all waits for the config job to publish.
var newCommitWait = 2 * time.Minute

// waitForNewCommit returns once project's commit on branch differs from
// before (the config job published), or after wait.
func waitForNewCommit(ctx context.Context, tns tnsIface.SimpleIface, project, branch, before string, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		if commit, _, err := tns.Commit(project, branch); err == nil && commit != "" && commit != before {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
}

// waitForJob returns true once repo has a job created at or after since that
// has finished, or false after wait (no such job: it was skipped as built).
func waitForJob(ctx context.Context, p patrickIface.Client, repo int, since int64, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		if j := newestJobs(p)[repo]; j != nil && j.Timestamp >= since &&
			j.Status != patrickIface.JobStatusOpen && j.Status != patrickIface.JobStatusLocked {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
}

// repoProject is the project a repository belongs to, or "" when neither auth
// nor TNS knows it yet; such a repository is pushed, as it always was.
func repoProject(auth authIface.Client, tns tnsIface.Client, repoId string) string {
	if id, err := strconv.Atoi(repoId); err == nil {
		if r, err := auth.Repositories().Github().Get(id); err == nil && r.Project() != "" {
			return r.Project()
		}
	}
	project, _ := tns.Simple().GetRepositoryProjectId("github", repoId)
	return project
}

// repoKindOrder sorts a project's repositories as their jobs depend on each
// other: config, then libraries, code and websites.
func repoKindOrder(fullName string) int {
	name := fullName[strings.LastIndex(fullName, "/")+1:]
	switch {
	case strings.HasPrefix(name, "tb_library_"):
		return 1
	case strings.HasPrefix(name, "tb_code_"):
		return 2
	case strings.HasPrefix(name, "tb_website_"):
		return 3
	default:
		return 0
	}
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
	pushedAt := int64(0)
	if len(params) > 5 {
		if p, ok := params[5].(int64); ok {
			pushedAt = p
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

	// Try to register. Registering returns once it is done, so wait only until
	// auth answers for the repository, instead of a second per repository.
	commonTest.RegisterTestRepositories(u.Context(), auth, commonTest.Repository{
		ID:   intRepoId,
		Name: strings.Split(fullname, "/")[1],
	})
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, err := auth.Repositories().Github().Get(intRepoId); err == nil {
			break
		}
	}

	var newPayload []byte
	if localPath != "" {
		if fi, err := os.Stat(localPath); err != nil || !fi.IsDir() {
			return fmt.Errorf("local path is not an existing directory: %s", localPath)
		}
		commitID, err := commonTest.HeadCommitFromLocalRepo(localPath)
		if err != nil {
			return fmt.Errorf("head commit from local repo: %w", err)
		}
		newPayload, err = commonTest.MakeTemplate(intRepoId, fullname, branch, commitID, "local://"+localPath, pushedAt)
		if err != nil {
			return fmt.Errorf("make template failed with: %v", err)
		}
	} else {
		newPayload, err = commonTest.MakeTemplate(intRepoId, fullname, branch, "", "", pushedAt)
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
			payload, err := commonTest.MakeTemplate(commonTest.ConfigRepo.ID, fullname, spec.DefaultBranches[0], commitID, "local://"+localPath, 0)
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
			payload, err := commonTest.MakeTemplate(commonTest.CodeRepo.ID, fullname, spec.DefaultBranches[0], commitID, "local://"+localPath, 0)
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
			payload, err := commonTest.MakeTemplate(commonTest.WebsiteRepo.ID, fullname, spec.DefaultBranches[0], commitID, "local://"+localPath, 0)
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
			payload, err := commonTest.MakeTemplate(commonTest.LibraryRepo.ID, fullname, spec.DefaultBranches[0], commitID, "local://"+localPath, 0)
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
