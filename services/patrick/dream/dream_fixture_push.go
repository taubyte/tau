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

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/core/kvdb"
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
	dream.RegisterFixture("pushSpecific", pushSpecificOnce)
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
	kv := u.Patrick().KV()
	if err := simple.Provides("patrick"); err == nil {
		if p, err := simple.Patrick(); err == nil {
			patrick = p
			if projectId != "" {
				patrick = projectJobs{Client: p, ctx: u.Context(), kv: kv, prefix: "/by/project/" + projectId + "/"}
			}
			if !force {
				jobs = withUnclaimed(newestJobs(patrick), unclaimedMarks(u.Context(), kv))
			}
		}
	}

	own := projectRepoIDs(u.Context(), auth, tns, projectId, branch)
	if projectId != "" && own == nil {
		return fmt.Errorf("auth does not know project %s: nothing was pushed", projectId)
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
			if !inProject(own, projectId, repoId) {
				continue
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
		owner := claimedBy(own, projectId, repoId, func() string { return repoProject(auth, tns, repoId) })
		if !markPushed(pushed, jobs, repoId, owner) {
			fmt.Printf("Skipping %s: it already has a job and no project claims it yet\n", fullName)
			return nil
		}
		if jobs != nil {
			noteUnclaimed(u.Context(), kv, repoId, owner)
		}
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
	// That wait is up to newCommitWait, as long as tau waits for this whole call,
	// so it happens after the caller has its answer: the jobs are already queued.
	go func() {
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
			if own != nil {
				own[r.id] = true // its published config names it
			}
			if err := push(repo(r)); err != nil {
				fmt.Printf("Pushing %s failed: %s\n", r.fullName, err)
			}
		}
	}()

	return nil
}

// projectJobs answers List and Get from one project's jobs in Patrick's store.
type projectJobs struct {
	patrickIface.Client
	ctx    context.Context
	kv     kvdb.KVDB
	prefix string
}

func (p projectJobs) List() ([]string, error) {
	keys, err := p.kv.List(p.ctx, p.prefix)
	if err != nil {
		return nil, err
	}
	for i, key := range keys {
		keys[i] = strings.TrimPrefix(key, p.prefix)
	}
	return keys, nil
}

func (p projectJobs) Get(id string) (*patrickIface.Job, error) {
	data, err := p.kv.Get(p.ctx, "/archive/jobs/"+id)
	if err != nil {
		if data, err = p.kv.Get(p.ctx, "/jobs/"+id); err != nil {
			return nil, err
		}
	}
	job := new(patrickIface.Job)
	if err := cbor.Unmarshal(data, job); err != nil {
		return nil, err
	}
	return job, nil
}

// authProjectWait is how long push-all waits for Auth to answer for a project:
// just after Dream starts, Auth does not answer yet.
var authProjectWait = 30 * time.Second

// waitForAuthProject asks get until it has the project, for up to wait.
func waitForAuthProject(ctx context.Context, get func() *authIface.Project, wait time.Duration) *authIface.Project {
	deadline := time.Now().Add(wait)
	for {
		if p := get(); p != nil || time.Now().After(deadline) {
			return p
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}

// projectRepoIDs is what a project owns, by Auth and its published config. It
// is nil without a project id, or when Auth does not know the project.
func projectRepoIDs(ctx context.Context, auth authIface.Client, tns tnsIface.Client, projectId, branch string) map[string]bool {
	if projectId == "" {
		return nil
	}
	p := waitForAuthProject(ctx, func() *authIface.Project { return auth.Projects().Get(projectId) }, authProjectWait)
	if p == nil {
		return nil
	}
	own := map[string]bool{strconv.Itoa(p.Git.Config.Id()): true, strconv.Itoa(p.Git.Code.Id()): true}
	websites, _, _, _ := tns.Website().All(projectId, "", branch).List()
	libraries, _, _, _ := tns.Library().All(projectId, "", branch).List()
	for _, r := range configuredRepos(websites, libraries) {
		own[r.id] = true
	}
	return own
}

// inProject says whether a push-all for projectId takes repoId. A repository no
// project claims is nobody's to build: taking it into every project's push-all
// is what re-created its failing job for hours.
func inProject(own map[string]bool, projectId, repoId string) bool {
	return projectId == "" || own[repoId]
}

// claimedBy is who claims repoId. A repository its project owns is claimed even
// when a lookup has nothing yet, or a new project's repositories never get a job.
func claimedBy(own map[string]bool, projectId, repoId string, lookup func() string) func() string {
	return func() string {
		if own[repoId] {
			return projectId
		}
		return lookup()
	}
}

const unclaimedPrefix = "/dream/unclaimed/"

// noteUnclaimed records that repoId got a job while no project claims it. Such
// a job is indexed under no project, so the job list cannot show it.
func noteUnclaimed(ctx context.Context, kv kvdb.KVDB, repoId string, project func() string) {
	if project() == "" {
		if err := kv.Put(ctx, unclaimedPrefix+repoId, []byte{}); err != nil {
			fmt.Printf("Recording unclaimed repository %s failed: %s\n", repoId, err)
		}
	}
}

func alreadyPushedUnclaimed(ctx context.Context, kv kvdb.KVDB, repoId string, project func() string) bool {
	if _, err := kv.Get(ctx, unclaimedPrefix+repoId); err != nil {
		return false
	}
	return project() == ""
}

// unclaimedMarks lists the repositories noteUnclaimed recorded.
func unclaimedMarks(ctx context.Context, kv kvdb.KVDB) []string {
	keys, _ := kv.List(ctx, unclaimedPrefix)
	for i, key := range keys {
		keys[i] = strings.TrimPrefix(key, unclaimedPrefix)
	}
	return keys
}

// withUnclaimed adds a stand-in job for each marked repository without one.
func withUnclaimed(jobs map[int]*patrickIface.Job, marked []string) map[int]*patrickIface.Job {
	for _, repoId := range marked {
		id, err := strconv.Atoi(repoId)
		if err != nil {
			continue
		}
		if jobs == nil {
			jobs = make(map[int]*patrickIface.Job)
		}
		if jobs[id] == nil {
			jobs[id] = &patrickIface.Job{}
		}
	}
	return jobs
}

// pushSpecificOnce is the push-specific fixture: pushSpecific, capped at one job
// for a repository no project claims, as push-all is.
func pushSpecificOnce(u *dream.Universe, params ...interface{}) error {
	repoId, ok := "", false
	if len(params) > 0 {
		repoId, ok = params[0].(string)
	}
	// A malformed call is refused by pushSpecific, even for a repository already pushed.
	if !ok || len(params) < 2 {
		return pushSpecific(u, params...)
	}
	if err := u.Provides("auth", "patrick", "tns"); err != nil {
		return err
	}
	simple, err := u.Simple("client")
	if err != nil {
		return fmt.Errorf("failed getting client with: %v", err)
	}
	if err = simple.Provides("auth", "tns"); err != nil {
		return err
	}
	auth, err := simple.Auth()
	if err != nil {
		return err
	}
	tns, err := simple.TNS()
	if err != nil {
		return err
	}
	owner := func() string { return repoProject(auth, tns, repoId) }
	kv := u.Patrick().KV()
	if alreadyPushedUnclaimed(u.Context(), kv, repoId, owner) {
		fmt.Printf("Skipping repository %s: it already has a job and no project claims it yet\n", repoId)
		return nil
	}
	if err = pushSpecific(u, params...); err == nil {
		noteUnclaimed(u.Context(), kv, repoId, owner)
	}
	return err
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

// hasJobButNoProject is true for a repository that already got a job and still
// belongs to no project. Every push-all gives a repository a new job id, and
// such a job can only fail, so one is enough: it is pushed again once a project
// claims it, or with force, which leaves jobs nil.
func hasJobButNoProject(jobs map[int]*patrickIface.Job, repoId string, project func() string) bool {
	id, err := strconv.Atoi(repoId)
	return err == nil && jobs[id] != nil && project() == ""
}

// markPushed records repoId as pushed and returns true, unless it is skipped as
// having a job and no project; then a later pass of the same push-all, after the
// config job, can still push it.
func markPushed(pushed map[string]bool, jobs map[int]*patrickIface.Job, repoId string, project func() string) bool {
	if hasJobButNoProject(jobs, repoId, project) {
		return false
	}
	pushed[repoId] = true
	return true
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

	if len(projectId) == 0 {
		projectId = commonTest.ProjectID
	}

	return pushWrapperFor(u, newPayload, commonTest.Repository{ID: intRepoId, Name: strings.Split(fullname, "/")[1]}, projectId)
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
	return pushWrapperFor(u, gitPayload, repo, commonTest.ProjectID)
}

func pushWrapperFor(u *dream.Universe, gitPayload []byte, repo commonTest.Repository, projectID string) error {
	if err := u.Provides("auth", "patrick", "tns"); err != nil {
		return fmt.Errorf("unable to provide: %w", err)
	}

	mockPatrickURL, err := u.GetURLHttp(u.Patrick().Node())
	if err != nil {
		return fmt.Errorf("unable to get url http: %w", err)
	}

	servicesCommon.FakeSecret = true
	fmt.Printf("Pushing job to from repo %s. ProjectID: %s\n", repo.Name, projectID)

	if err := commonTest.PushJobFor(projectID, gitPayload, mockPatrickURL, repo); err != nil {
		return fmt.Errorf("unable to push job: %w", err)
	}

	return nil
}
