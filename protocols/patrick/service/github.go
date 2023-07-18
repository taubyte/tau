package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/go-interfaces/services/http"
	"github.com/taubyte/go-interfaces/services/patrick"
	patrickSpecs "github.com/taubyte/go-specs/patrick"
	protocolsCommon "github.com/taubyte/odo/protocols/common"
	"github.com/taubyte/utils/id"
	"gopkg.in/go-playground/webhooks.v5/github"
)

func (srv *PatrickService) githubCheckHookAndExtractSecret(ctx http.Context) (interface{}, error) {
	if protocolsCommon.FakeSecret && srv.devMode {
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
	newJob := &patrick.Job{
		Status:    patrick.JobStatusOpen,
		Timestamp: time.Now().Unix(),
		Logs:      make(map[string]string),
		AssetCid:  make(map[string]string),
		Attempt:   0,
	}

	secret, err := ctx.GetStringVariable("GithubSecret") // comes from auth
	if err != nil {
		return nil, err
	}

	if protocolsCommon.DelayJob {
		newJob.Delay = &patrick.DelayConfig{
			Time: int(protocolsCommon.DelayJobTime),
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
		logger.Debug(map[string]interface{}{"msg": fmt.Sprintf("Hook triggred. Push: %v", payload)})
		pl, err := json.Marshal(payload)
		if err != nil {
			logger.Error(map[string]interface{}{"msg": fmt.Sprintf("Got %v when creating pipeline for %s ", err, payload)})
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

		logger.Info(map[string]interface{}{"message": "SOmething", "payload": string(pl), "job": newJob.Meta})

		// Setting current branch and main branch
		if newJob.Meta.Repository.MainBranch == "" {
			newJob.Meta.Repository.MainBranch = newJob.Meta.Repository.Branch
		}
		newJob.Meta.Repository.Branch = strings.Replace(newJob.Meta.Ref, "refs/heads/", "", 1)

		if newJob.Meta.Repository.MainBranch != newJob.Meta.Repository.Branch && !srv.devMode {
			return nil, fmt.Errorf("only builds main branch `%s` got `%s`", newJob.Meta.Repository.MainBranch, newJob.Meta.Repository.Branch)
		}

		err = srv.RegisterJob(ctx.Request().Context(), newJob)
		if err != nil {
			return nil, err
		}

		// Pushing useful information to tns for auth
		repoInfo := map[string]string{
			"id":  fmt.Sprintf("%d", newJob.Meta.Repository.ID),
			"ssh": newJob.Meta.Repository.SSHURL,
		}

		err = srv.tnsClient.Push([]string{"resolve", "repo", "github", fmt.Sprintf("%d", newJob.Meta.Repository.ID)}, repoInfo)
		if err != nil {
			return nil, fmt.Errorf("failed registering new job repo %d into tns with error: %v", newJob.Meta.Repository.ID, err)
		}

		return newJob, nil
	/*case github.ReleasePayload:
	release := payload.(github.ReleasePayload)
	// Do whatever you want from here...
	fmt.Printf("%+v", release)
	*/
	default:
		return nil, fmt.Errorf("this is not a push event. but a %T", payload)
	}
}

func (srv *PatrickService) RegisterJob(ctx context.Context, newJob *patrick.Job) error {
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
