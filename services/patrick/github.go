package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	authClient "github.com/taubyte/tau/clients/p2p/auth"
	authIface "github.com/taubyte/tau/core/services/auth"
	iface "github.com/taubyte/tau/core/services/patrick"
	http "github.com/taubyte/tau/pkg/http"
	patrickSpecs "github.com/taubyte/tau/pkg/specs/patrick"
	servicesCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/utils/fs/dir"
	"github.com/taubyte/tau/utils/id"
	"gopkg.in/go-playground/webhooks.v5/github"

	commonSpec "github.com/taubyte/tau/pkg/specs/common"
)

// GitHub webhook handlers

func (srv *PatrickService) githubCheckHookAndExtractSecret(ctx http.Context) (interface{}, error) {
	if servicesCommon.FakeSecret && srv.devMode {
		ctx.SetVariable("GithubSecret", "taubyte_secret")
		return nil, nil
	}
	hook_uuid, err := ctx.GetStringVariable("hook")
	if err != nil {
		return nil, fmt.Errorf("get string context failed with %w", err)
	}

	hook, err := srv.getHook(hook_uuid)
	if err != nil {
		return nil, fmt.Errorf("get hook failed with %w", err)
	}

	github_hook, err := hook.Github()
	if err != nil {
		return nil, fmt.Errorf("github hook failed with %w", err)
	}
	ctx.SetVariable("GithubSecret", github_hook.Secret)

	return nil, nil
}

func (srv *PatrickService) githubHookHandler(ctx http.Context) (interface{}, error) {
	newJob := &iface.Job{
		Status:    iface.JobStatusOpen,
		Timestamp: time.Now().Unix(),
		Logs:      make(map[string]string),
		AssetCid:  make(map[string]string),
		Attempt:   0,
	}

	secret, err := ctx.GetStringVariable("GithubSecret") // comes from auth
	if err != nil {
		return nil, err
	}

	if servicesCommon.DelayJob {
		newJob.Delay = &iface.DelayConfig{
			Time: int(servicesCommon.DelayJobTime),
		}
	}

	hook, err := github.New(github.Options.Secret(secret))
	if err != nil {
		return nil, fmt.Errorf("creating hook failed with %w", err)
	}

	// FIXME: move this logic to taubyte/http
	req := ctx.Request()
	req.Body = io.NopCloser(bytes.NewReader(ctx.Body()))

	payload, err := hook.Parse(ctx.Request(), github.PushEvent)
	if err != nil {
		if err == github.ErrEventNotFound {
			// ok event wasn't one of the ones asked to be parsed
			return nil, errors.New("this is not a push event")
		}
		return nil, fmt.Errorf("parsing hook failed with %w", err)
	}

	switch payload.(type) {
	case github.PushPayload:
		logger.Debugf("Hook triggred. Push: %v", payload)
		pl, err := json.Marshal(payload)
		if err != nil {
			logger.Errorf("new pipeline for payload `%#v` failed with: %s", payload, err.Error())
			return nil, errors.New("can't decode push payload")
		}

		//Unmarshal the needed json fields into the structure
		err = json.Unmarshal(pl, &newJob.Meta)
		if err != nil {
			return nil, fmt.Errorf("failed unmarshalling payload into struct with error: %w", err)
		}
		job_id := id.Generate(newJob.Meta.Repository.ID)

		//Assign fields before marshal
		newJob.Meta.Repository.Provider = "github"
		newJob.Id = job_id

		newJob.Meta.Repository.Branch = strings.Replace(newJob.Meta.Ref, "refs/heads/", "", 1)

		if !slices.Contains(commonSpec.DefaultBranches, newJob.Meta.Repository.Branch) && !srv.devMode {
			return nil, fmt.Errorf("only builds main branches %v got `%s`", commonSpec.DefaultBranches, newJob.Meta.Repository.Branch)
		}

		// Pushing useful information to tns
		repoInfo := map[string]string{
			"id":  fmt.Sprintf("%d", newJob.Meta.Repository.ID),
			"ssh": newJob.Meta.Repository.SSHURL,
		}

		err = srv.tnsClient.Push([]string{"resolve", "repo", "github", fmt.Sprintf("%d", newJob.Meta.Repository.ID)}, repoInfo)
		if err != nil {
			return nil, fmt.Errorf("failed registering new job repo %d into tns with error: %v", newJob.Meta.Repository.ID, err)
		}

		err = srv.RegisterJob(ctx.Request().Context(), newJob)
		if err != nil {
			return nil, fmt.Errorf("failed registering job with error: %w", err)
		}

		logger.Debugf("Got job: %#v", newJob)

		return newJob, nil
	default:
		return nil, fmt.Errorf("this is not a push event. but a %T", payload)
	}
}

func (srv *PatrickService) RegisterJob(ctx context.Context, newJob *iface.Job) error {
	job_byte, err := cbor.Marshal(newJob)
	if err != nil {
		return fmt.Errorf("failed cbor marshall on job structure with err: %w", err)
	}

	// Store the job inside the database with a generated ID
	err = srv.db.Put(ctx, "/jobs/"+newJob.Id, job_byte)
	if err != nil {
		return fmt.Errorf("failed putting job into database with error: %w", err)
	}

	err = srv.connectToProject(ctx, newJob)
	if err != nil {
		return err
	}

	// Send the job over pub sub
	err = srv.node.PubSubPublish(ctx, patrickSpecs.PubSubIdent, job_byte)
	if err != nil {
		return fmt.Errorf("failed to send over pubsub error: %w", err)
	}

	return nil
}

// Hook management
func (srv *PatrickService) getHook(hookid string) (authIface.Hook, error) {
	return srv.authClient.Hooks().Get(hookid)
}

// Git repository operations

type GitRepository struct {
	provider string
	id       string
	path     dir.Directory
	info     authIface.Repository
	key      *ssh.PublicKeys
	repo     *git.Repository
	logger   io.Writer
}

func (srv *PatrickService) NewGitRepository(provider string, repositoryId string, output io.Writer) (iface.GitRepository, error) {
	var (
		gr  GitRepository
		err error
	)

	gr.id = repositoryId
	gr.provider = provider
	gr.logger = output

	switch provider {
	case "github":
		id, err := strconv.Atoi(repositoryId)
		if err != nil {
			return nil, fmt.Errorf("failed str Atoi with error: %w", err)
		}

		repoInfo, err := srv.authClient.Repositories().Github().Get(id)
		if err != nil {
			return nil, fmt.Errorf("failed get repo's with error: %w", err)
		}

		gr.info = repoInfo

	default:
		gr.logger.Write([]byte("Error processing repository. `" + provider + "` not supported!"))
		return nil, errors.New("fnknown git provider")
	}

	gr.key, err = ssh.NewPublicKeys("git", []byte(gr.info.PrivateKey()), "")
	if err != nil {
		gr.logger.Write([]byte("Invalid git key. Error: " + err.Error()))
		return nil, errors.New("failed generating ssh key for git")
	}

	return &gr, nil
}

func (gr *GitRepository) Url() *string {
	switch gr.info.(type) {
	case *authClient.GithubRepository:
		return &(gr.info.(*authClient.GithubRepository).Url)
	default:
		return nil
	}
}

func (gr *GitRepository) Clone(ctx context.Context, path string, ref string) error {
	url := gr.Url()
	if url == nil {
		return errors.New("repository does not have a URL")
	}

	var err error

	gr.path = dir.Directory(path)

	gr.repo, err = git.PlainCloneContext(ctx, gr.Path(), false, &git.CloneOptions{
		// The intended use of a GitHub personal access token is in replace of your password
		// because access tokens can easily be revoked.
		// https://help.github.com/articles/creating-a-personal-access-token-for-the-command-line/
		Auth:          gr.key,
		URL:           *url,
		Progress:      gr.logger,
		SingleBranch:  true,
		Depth:         1,
		ReferenceName: plumbing.ReferenceName(ref),
	})

	if err != nil {
		gr.logger.Write([]byte("Cloning failed with error: " + err.Error()))
		gr.path.Remove()
		gr.path = ""
	}

	return err

}

func (gr *GitRepository) Path() string {
	return gr.path.Path()
}
