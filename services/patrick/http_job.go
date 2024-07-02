package service

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/h2non/filetype"
	"github.com/h2non/filetype/matchers"
	http "github.com/taubyte/http"
	"github.com/taubyte/tau/core/services/patrick"
	patrickSpecs "github.com/taubyte/tau/pkg/specs/patrick"
	servicesCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/utils/maps"
)

type project struct {
	ProjectId string
	JobIds    []string
}

func (srv *PatrickService) projectAllJobHandler(ctx http.Context) (interface{}, error) {
	projectId, err := maps.String(ctx.Variables(), "projectId")
	if err != nil {
		return []string{}, err
	}

	// TODO: Later use Async coz job list might grow big
	prefix := fmt.Sprintf("/by/project/%s/", projectId)
	res, err := srv.db.List(ctx.Request().Context(), prefix)
	if err != nil {
		return []string{}, fmt.Errorf("get failed with: %w", err)
	}

	jids := make([]string, len(res))
	for idx, jobKey := range res {
		jids[idx] = strings.TrimPrefix(jobKey, prefix)
	}

	data := project{
		ProjectId: projectId,
		JobIds:    jids,
	}

	return data, nil
}

func (srv *PatrickService) projectJobHandler(ctx http.Context) (iface interface{}, err error) {
	jid, err := maps.String(ctx.Variables(), "jid")
	if err != nil {
		return
	}

	var job *patrick.Job
	requestCtx := ctx.Request().Context()

	// Try getting from /archive/jobs/ if not found try  /jobs/
	job, err = srv.getJob(requestCtx, "/archive/jobs/", jid)
	if err != nil {
		job, err = srv.getJob(requestCtx, "/jobs/", jid)
		if err != nil {
			return
		}

	}

	return map[string]interface{}{"job": job}, nil
}

func (srv *PatrickService) cancelJob(ctx http.Context) (iface interface{}, err error) {
	jid, err := maps.String(ctx.Variables(), "jid")
	if err != nil {
		return
	}

	requestCtx := ctx.Request().Context()

	// Make sure that the job is not already archived as finished
	_, err = srv.db.Get(requestCtx, "/archive/jobs/"+jid)
	if err == nil {
		// No error means it found the job in archive.
		return nil, fmt.Errorf("job %s already finished, cannot cancel", jid)
	}

	// Get lock data to see who locked it
	lockBytes, err := srv.db.Get(requestCtx, "/locked/jobs/"+jid)
	if err != nil {
		/* Monkey has not picked up the job yet so we create a delay and wait
		   First make sure that the job is actually registered.
		   If we cant find the job registered we return error
		*/
		_, err := srv.db.Get(requestCtx, "/jobs/"+jid)
		if err != nil {
			return nil, fmt.Errorf("job %s is not registered", jid)
		}

		// We create a timer that keeps until a monkey gets the job.
		var attempts int
		for {

			lockBytes, err = srv.db.Get(requestCtx, "/locked/jobs/"+jid)
			if err != nil {
				attempts++
				if attempts == servicesCommon.MaxCancelAttempts {
					return nil, fmt.Errorf("failed cancelling job %s max attempts exceeded", jid)
				}
				time.Sleep(2 * time.Second)
			} else {
				break
			}
		}

	}

	var jobLock Lock
	err = cbor.Unmarshal(lockBytes, &jobLock)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshall job %s with %w", jid, err)
	}

	// Send directly to that monkey to cancel the job
	_, err = srv.monkeyClient.Peers(jobLock.Pid).Cancel(jid)
	if err != nil {
		return nil, fmt.Errorf("failed cancelling job %s on monkey with %w", jid, err)
	}

	return map[string]interface{}{"cancelled": jid}, nil
}

func (srv *PatrickService) retryJob(ctx http.Context) (iface interface{}, err error) {
	// Get job id
	jid, err := ctx.GetStringVariable("jid")
	if err != nil {
		return nil, fmt.Errorf("failed finding map jid with %w", err)
	}

	requestCtx := ctx.Request().Context()

	// This is just to make the successful job set to fail to retry it for testing on go-patrick-http test
	if srv.devMode && servicesCommon.RetryJob {
		// Get the specific job from archived
		job, err := srv.getJob(requestCtx, "/archive/jobs/", jid)
		if err != nil {
			return nil, fmt.Errorf("failed grabbing archived job %s with %w", jid, err)
		}

		if job.Status == patrick.JobStatusSuccess {
			job.Status = patrick.JobStatusFailed
			override, err := cbor.Marshal(job)
			if err != nil {
				return nil, fmt.Errorf("cbor marshall on job %s failed with err: %w", job.Id, err)
			}

			err = srv.db.Put(requestCtx, "/archive/jobs/"+job.Id, override)
			if err != nil {
				return nil, err
			}
		}
	}

	// Get the specific job from archived
	job, err := srv.getJob(requestCtx, "/archive/jobs/", jid)
	if err != nil {
		return nil, fmt.Errorf("failed grabbing archived job %s with %w", jid, err)
	}

	// Only retry if it was cancelled or failed
	if job.Status == patrick.JobStatusCancelled || job.Status == patrick.JobStatusFailed || job.Status == patrick.JobStatusSuccess {
		// Change to open and resend over pubsub
		job.Status = patrick.JobStatusOpen
		job_byte, err := cbor.Marshal(job)
		if err != nil {
			logger.Errorf("failed cbor marshall on job %s with err: %w", job.Id, err)
			return nil, fmt.Errorf("failed marshalling job %s with err %w", job.Id, err)
		}

		// Send the job over pub sub
		err = srv.node.PubSubPublish(requestCtx, patrickSpecs.PubSubIdent, job_byte)
		if err != nil {
			return nil, fmt.Errorf("failed to send over pubsub error: %v", err)
		}

		// Put the job back into the list
		err = srv.db.Put(requestCtx, "/jobs/"+job.Id, job_byte)
		if err != nil {
			logger.Errorf("failed putting job %s into database with error: %s", job.Id, err.Error())
			return nil, fmt.Errorf("failed putting job %s with %w", job.Id, err)
		}

		// Delete from archive jobs now that it's back out as open
		err = srv.db.Delete(requestCtx, "/archive/jobs/"+jid)
		if err != nil {
			return nil, fmt.Errorf("failed deleting job %s with error: %w", jid, err)
		}

		return map[string]interface{}{"retry": job.Id}, nil
	}

	return nil, nil
}

func (srv *PatrickService) getJob(ctx context.Context, loc string, jid string) (job *patrick.Job, err error) {
	jobByte, err := srv.db.Get(ctx, loc+jid)
	if err != nil {
		err = fmt.Errorf("get job %s failed with: %w", jid, err)
		return
	}

	err = cbor.Unmarshal(jobByte, &job)
	if err != nil {
		err = fmt.Errorf("unmarshal job %s failed with %w", jid, err)
		return
	}

	return
}

func (srv *PatrickService) cidHandler(ctx http.Context) (interface{}, error) {
	cid, err := ctx.GetStringVariable("cid")
	if err != nil {
		return nil, err
	}

	f, err := srv.node.GetFile(ctx.Request().Context(), cid)
	if err != nil {
		return nil, err
	}

	return http.RawStream{ContentType: "application/text", Stream: f}, nil
}

func (srv *PatrickService) downloadAsset(ctx http.Context) (interface{}, error) {
	// Get job id
	jobId, err := ctx.GetStringVariable("jobId")
	if err != nil {
		return nil, fmt.Errorf("failed finding jobId with %w", err)
	}

	// Get resource id
	resourceId, err := ctx.GetStringVariable("resourceId")
	if err != nil {
		return nil, fmt.Errorf("failed finding resourceId with %w", err)
	}

	job, err := srv.getJob(ctx.Request().Context(), "/archive/jobs/", jobId)
	if err != nil {
		return nil, fmt.Errorf("failed finding job %s with %w", jobId, err)
	}

	assetCid, ok := job.AssetCid[resourceId]
	if !ok {
		return nil, fmt.Errorf("job %s doest not have an asset for resourceId %s", jobId, resourceId)
	}

	file, err := srv.node.GetFile(ctx.Request().Context(), assetCid)
	if err != nil {
		return nil, fmt.Errorf("failed grabbing asset cid %s with %v", assetCid, err)
	}
	defer file.Close()

	typeBuff := make([]byte, 512)
	if _, err := file.Read(typeBuff); err != nil {
		return nil, fmt.Errorf("rewinding asset file %s failed with %s", assetCid, err)
	}

	if _, err := file.Seek(0, 0); err != nil {
		return nil, fmt.Errorf("rewinding asset file %s failed with %s", assetCid, err)
	}

	fileData, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed reading asset file %s with %v", assetCid, err)
	}

	contentType, err := filetype.Match(typeBuff)
	if err != nil {
		return nil, fmt.Errorf("failed filetype match for asset %s wtih %v", assetCid, err)
	}

	if contentType == matchers.TypeZip {
		return http.RawData{ContentType: "application/zip", Data: fileData}, nil
	} else {
		return http.RawData{ContentType: "application/wasm", Data: fileData}, nil
	}
}
