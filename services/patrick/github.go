package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	authIface "github.com/taubyte/tau/core/services/auth"
	iface "github.com/taubyte/tau/core/services/patrick"
	http "github.com/taubyte/tau/pkg/http"
	servicesCommon "github.com/taubyte/tau/services/common"
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

	// Dream's inject signs only SHA-1 with the fake secret: keep the library's check for it.
	parseSecret := ""
	if servicesCommon.FakeSecret && srv.devMode {
		parseSecret = secret
	} else {
		if secret == "" && !srv.devMode {
			return nil, errors.New("hook has no secret")
		}
		if secret != "" && !validSignature256(ctx.Request().Header.Get("X-Hub-Signature-256"), ctx.Body(), secret) {
			return nil, errors.New("invalid X-Hub-Signature-256")
		}
	}

	hook, err := github.New(github.Options.Secret(parseSecret))
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
		logger.Debugf("Hook triggered: push")
		pl, err := json.Marshal(payload)
		if err != nil {
			logger.Errorf("encoding push payload failed with: %s", err.Error())
			return nil, errors.New("can't decode push payload")
		}

		// Unmarshal the needed json fields into the structure (Repository.UnmarshalJSON runs normalize)
		err = json.Unmarshal(pl, &newJob.Meta)
		if err != nil {
			return nil, fmt.Errorf("failed unmarshalling payload into struct with error: %w", err)
		}

		// Assign fields before id and branch checks
		newJob.Meta.Repository.Provider = "github"
		newJob.Meta.Repository.Branch = strings.Replace(newJob.Meta.Ref, "refs/heads/", "", 1)

		if !slices.Contains(commonSpec.DefaultBranches, newJob.Meta.Repository.Branch) && !srv.devMode {
			return nil, fmt.Errorf("only builds main branches %v got `%s`", commonSpec.DefaultBranches, newJob.Meta.Repository.Branch)
		}

		newJob.Id = iface.PushEventJobID(&newJob.Meta)

		// a redelivered push, even of a finished job, returns that job and runs nothing
		if existing, err := srv.anyJob(ctx.Request().Context(), newJob.Id); err == nil && existing != nil {
			return existing, nil
		}

		if !srv.devMode && time.Since(time.Unix(newJob.Meta.Repository.PushedAt, 0)) > maxPushAge {
			return nil, fmt.Errorf("push is older than %s; use retry to rebuild it", maxPushAge)
		}

		// Pushing useful information to tns (ssh key stores effective URI for backward compat)
		repoInfo := map[string]string{
			"id":  fmt.Sprintf("%d", newJob.Meta.Repository.ID),
			"ssh": newJob.Meta.Repository.URI,
		}

		err = srv.tnsClient.Push([]string{"resolve", "repo", "github", fmt.Sprintf("%d", newJob.Meta.Repository.ID)}, repoInfo)
		if err != nil {
			return nil, fmt.Errorf("failed registering new job repo %d into tns with error: %v", newJob.Meta.Repository.ID, err)
		}

		err = srv.RegisterJob(ctx.Request().Context(), newJob)
		if err != nil {
			return nil, fmt.Errorf("failed registering job with error: %w", err)
		}

		logger.Debugf("Got job: %s", newJob.Id)

		return newJob, nil
	default:
		return nil, fmt.Errorf("this is not a push event. but a %T", payload)
	}
}

// maxPushAge refuses a signed push whose job is no longer stored (a replay).
var maxPushAge = 24 * time.Hour

// validSignature256 checks GitHub's "sha256=<hex hmac>" header over the raw body.
func validSignature256(header string, body []byte, secret string) bool {
	sig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

func (srv *PatrickService) RegisterJob(ctx context.Context, newJob *iface.Job) error {
	if _, err := srv.db.Get(ctx, "/jobs/"+newJob.Id); err == nil {
		return nil
	}

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

	exists, err := srv.jobExistsInOtherCluster(ctx, newJob.Id)
	if err != nil {
		return fmt.Errorf("cross-cluster check failed: %w", err)
	}
	if exists {
		return nil
	}
	if err = srv.enqueue(newJob.Id); err != nil {
		return fmt.Errorf("failed to push job onto queue: %w", err)
	}
	return nil
}

// Hook management
func (srv *PatrickService) getHook(hookid string) (authIface.Hook, error) {
	return srv.authClient.Hooks().Get(hookid)
}
