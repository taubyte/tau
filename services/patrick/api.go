package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/h2non/filetype"
	"github.com/h2non/filetype/matchers"
	"github.com/libp2p/go-libp2p/core/peer"
	commonIface "github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	patrickSpecs "github.com/taubyte/tau/pkg/specs/patrick"
	servicesCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/utils/maps"

	http "github.com/taubyte/tau/pkg/http"
	httpAuth "github.com/taubyte/tau/pkg/http/auth"
	authService "github.com/taubyte/tau/services/auth"
)

// Stream route setup
func (srv *PatrickService) setupStreamRoutes() {
	srv.stream.Define("ping", func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		return cr.Response{"time": int(time.Now().Unix())}, nil
	})
	srv.stream.Define("patrick", srv.requestServiceHandler)
	srv.stream.Define("stats", srv.statsServiceHandler)
}

// HTTP route setup
func (srv *PatrickService) setupHTTPRoutes() {
	// All github hooks will come through POST
	// see: https://github.com/go-playground/webhooks/blob/v5.17.0/github/github.go#L128
	srv.setupGithubRoutes()
	srv.setupJobRoutes()
}

// Stats service handler
func (p *PatrickService) statsServiceHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, err
	}

	switch action {
	case "db":
		return cr.Response{"stats": p.db.Stats(ctx).Encode()}, nil
	default:
		return nil, errors.New("stats action `" + action + "` not recognized")
	}
}

// Main request service handler
func (p *PatrickService) requestServiceHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	cidMap := make(map[string]string, 0)
	assetMap := make(map[string]string, 0)
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, fmt.Errorf("failed getting action from body with error: %v", err)
	}

	logCid := body["cid"]
	assetCid := body["assetCid"]

	if assetCid != nil {
		assetMap, err = convertToStringMap(assetCid)
		if err != nil {
			return nil, err
		}
	}
	if logCid != nil {
		cidMap, err = convertToStringMap(logCid)
		if err != nil {
			return nil, err
		}
	}

	jid, err := maps.String(body, "jid")
	if err != nil {
		return nil, fmt.Errorf("failed getting jid from body with error: %v", err)
	}
	switch action {
	case "list":
		return p.listHandler(ctx)
	case "info":
		return p.infoHandler(ctx, jid)
	case "lock":
		eta, err := maps.Int(body, "eta")
		if err != nil {
			return nil, fmt.Errorf("failed getting eta from body with error: %v", err)
		}

		return p.lockHandler(ctx, jid, int64(eta), conn)
	case "isLocked":
		return p.isLockedHandler(ctx, jid, conn)
	case "unlock":
		return p.unlockHandler(ctx, jid)
	case "cancel":
		return cr.Response{"cancelled": jid}, p.cancelHandler(ctx, jid, cidMap)
	case "done":
		return nil, p.doneHandler(ctx, jid, cidMap, assetMap, conn)
	case "failed":
		return nil, p.failedHandler(ctx, jid, cidMap, assetMap, conn)
	case "timeout":
		return nil, p.timeoutHandler(ctx, jid, cidMap)
	}

	return nil, nil
}

// List handler
func (p *PatrickService) listHandler(ctx context.Context) (cr.Response, error) {
	jobIds := make([]string, 0)
	jobs, err := p.db.List(ctx, "/jobs/")
	if err != nil {
		return nil, fmt.Errorf("failed getting jobs with error: %v", err)
	}

	jobIds = append(jobIds, jobs...)

	jobs, err = p.db.List(ctx, "/archive/jobs/")
	if err != nil {
		return nil, fmt.Errorf("failed getting archive jobs with error: %v", err)
	}

	jobIds = append(jobIds, jobs...)

	for index, id := range jobIds {
		list := strings.Split(id, "/jobs/")
		if len(list) > 1 {
			jobIds[index] = list[1]
		}
	}

	return cr.Response{"Ids": jobIds}, nil
}

// Info handler
func (p *PatrickService) infoHandler(ctx context.Context, jid string) (cr.Response, error) {
	var job commonIface.Job
	jobByte, err := p.db.Get(ctx, "/jobs/"+jid)
	if err != nil {
		jobByte, err = p.db.Get(ctx, "/archive/jobs/"+jid)
		if err != nil {
			return nil, fmt.Errorf("could not find %s in /archive/jobs or /jobs", jid)
		}
	}

	err = cbor.Unmarshal(jobByte, &job)
	if err != nil {
		return nil, fmt.Errorf("unmarshal job %s failed with %w", jid, err)
	}

	return cr.Response{"job": &job}, nil
}

// Lock handler
func (p *PatrickService) lockHandler(ctx context.Context, jid string, eta int64, conn streams.Connection) (cr.Response, error) {
	var lockData []byte
	lockData, err := p.db.Get(ctx, "/locked/jobs/"+jid)
	if err != nil {
		return p.tryLock(ctx, conn.RemotePeer(), jid, time.Now().Unix(), eta)
	} else {
		resp, err := p.lockHelper(ctx, conn.RemotePeer(), lockData, jid, eta, true)
		if err != nil {
			return nil, fmt.Errorf("error in lockHandler %w", err)
		}
		return resp, nil

	}

}

// Is locked handler
func (p *PatrickService) isLockedHandler(ctx context.Context, jid string, conn streams.Connection) (cr.Response, error) {
	lockData, err := p.db.Get(ctx, "/locked/jobs/"+jid)
	if err == nil {
		resp, err := p.lockHelper(ctx, conn.RemotePeer(), lockData, jid, 0, false)
		if resp != nil {
			return resp, err
		}
	}

	return cr.Response{"locked": false}, nil
}

// Unlock handler
func (p *PatrickService) unlockHandler(ctx context.Context, jid string) (cr.Response, error) {
	lockData, err := p.db.Get(ctx, "/locked/jobs/"+jid)
	if err != nil {
		return nil, err
	}
	var jobLock Lock
	if err = cbor.Unmarshal(lockData, &jobLock); err != nil {
		logger.Errorf("Unmarshal for `%s` failed with: %s", jid, err.Error())
		// fine, something might've happended. we can auto-heal -> locking the job will write correct data
	}

	jobLock.Eta = 0
	jobLock.Timestamp = 0
	lockBytes, err := cbor.Marshal(jobLock)
	if err != nil {
		logger.Errorf("Marshal for `%s` failed with: %s", jid, err.Error())
		return nil, fmt.Errorf("marshal for `%s` failed with: %w", jid, err)
	}

	err = p.db.Put(ctx, "/locked/jobs/"+jid, lockBytes)
	if err != nil {
		logger.Errorf("Putting locked job for `%s` failed with: %s", jid, err.Error())
		return nil, fmt.Errorf("putting locked job for `%s` failed with: %w", jid, err)
	}

	return cr.Response{"unlocked": jid}, nil
}

// Timeout handler
func (p *PatrickService) timeoutHandler(ctx context.Context, jid string, cid_log map[string]string) error {
	if _, err := p.getJob(ctx, "/archive/jobs/", jid); err != nil {
		job, err := p.getJob(ctx, "/jobs/", jid)
		if err != nil {
			return fmt.Errorf("failed finding job %s in timeoutHandler with %v", jid, err)
		}

		if job.Attempt == servicesCommon.MaxJobAttempts {
			job.Status = commonIface.JobStatusFailed
			job.Logs = cid_log

			jobData, err := cbor.Marshal(job)
			if err != nil {
				return fmt.Errorf("marshal in updateStatus error: %w", err)
			}

			err = p.db.Put(ctx, "/archive/jobs/"+jid, jobData)
			if err != nil {
				return fmt.Errorf("failed put in timeoutHandler with %w", err)
			}

			err = p.deleteJob(ctx, jid, "/locked/jobs/", "/jobs/")
			if err != nil {
				return fmt.Errorf("failed delete in timeoutHandler with %w", err)
			}

			return nil
		}

		job.Attempt++
		job.Timestamp = time.Now().Unix()
		job.Status = commonIface.JobStatusOpen

		if err = p.db.Delete(ctx, "/locked/jobs/"+jid); err != nil {
			return fmt.Errorf("failed deleting job %s in /locked/jobs/ with error: %w", jid, err)
		}

		job_bytes, err := cbor.Marshal(job)
		if err != nil {
			return fmt.Errorf("failed to marshal job %s with error: %w", jid, err)
		}

		if err = p.db.Put(ctx, "/jobs/"+jid, job_bytes); err != nil {
			return err
		}

		if err = p.node.PubSubPublish(ctx, patrickSpecs.PubSubIdent, job_bytes); err != nil {
			return fmt.Errorf("failed to send over pubsub error: %w", err)
		}

		return nil
	}

	return fmt.Errorf("%s already finished", jid)
}

// Done handler
func (p *PatrickService) doneHandler(ctx context.Context, jid string, cid_log map[string]string, assetCid map[string]string, conn streams.Connection) error {
	return p.updateStatus(ctx, conn.RemotePeer(), jid, cid_log, commonIface.JobStatusSuccess, assetCid)
}

// Failed handler
func (p *PatrickService) failedHandler(ctx context.Context, jid string, cid_log map[string]string, assetCid map[string]string, conn streams.Connection) error {
	return p.updateStatus(ctx, conn.RemotePeer(), jid, cid_log, commonIface.JobStatusFailed, assetCid)
}

// Cancel handler
func (p *PatrickService) cancelHandler(ctx context.Context, jid string, cid_log map[string]string) error {
	return p.updateStatus(ctx, "", jid, cid_log, commonIface.JobStatusCancelled, nil)
}

// Delete job helper
func (p *PatrickService) deleteJob(ctx context.Context, jid string, loc ...string) error {
	for _, _loc := range loc {
		if err := p.db.Delete(ctx, _loc+jid); err != nil {
			return fmt.Errorf("failed deleting %s at %s with %v", jid, _loc, err)
		}
	}

	return nil
}

// Lock helper
func (p *PatrickService) lockHelper(ctx context.Context, pid peer.ID, lockData []byte, jid string, eta int64, method bool) (cr.Response, error) {
	var jobLock Lock
	err := cbor.Unmarshal(lockData, &jobLock)
	if err != nil {
		logger.Errorf("Reading lock for `%s` failed with: %s", jid, err.Error())
		return nil, err
	} else if jobLock.Timestamp+jobLock.Eta > time.Now().Unix() {
		if method {
			if jobLock.Pid == pid {
				return p.tryLock(ctx, pid, jid, jobLock.Timestamp, eta)
			} else {
				return cr.Response{
					"locked":    true,
					"locked-by": jobLock.Pid.String(),
					"till":      jobLock.Timestamp + jobLock.Eta,
				}, fmt.Errorf("job is locked by `%s`", jobLock.Pid)
			}
		}

		return cr.Response{"locked": true, "locked-by": jobLock.Pid.String()}, nil
	}

	if method {
		return p.tryLock(ctx, pid, jid, jobLock.Timestamp, eta)
	}

	return cr.Response{"locked": false}, nil
}

// Try lock helper
func (p *PatrickService) tryLock(ctx context.Context, pid peer.ID, jid string, timestamp, eta int64) (cr.Response, error) {
	lockData, err := cbor.Marshal(Lock{
		Pid:       pid, // monkey ID
		Timestamp: timestamp,
		Eta:       eta,
	})
	if err != nil {
		return nil, fmt.Errorf("failed cbor marshal with error: %v", err)
	}

	if err = p.db.Put(ctx, ("/locked/jobs/" + jid), lockData); err != nil {
		return nil, fmt.Errorf("locking `%s` failed with: %v", jid, err)
	}

	return nil, nil
}

// Update status helper
func (p *PatrickService) updateStatus(ctx context.Context, pid peer.ID, jid string, cid_log map[string]string, status commonIface.JobStatus, assetCid map[string]string) error {

	if pid != "" {
		lockData, err := p.db.Get(ctx, "/locked/jobs/"+jid)
		if err == nil {
			var jobLock Lock
			if err := cbor.Unmarshal(lockData, &jobLock); err == nil {
				if pid != peer.ID(jobLock.Pid) {
					return fmt.Errorf("failed to update job %s, %s is not the owner", jid, pid)
				}
			}
		}
	}

	var job commonIface.Job
	// Grab job and move it to /archive/jobs/{jid}
	getJob, err := p.db.Get(ctx, "/jobs/"+jid)
	if err != nil {
		return fmt.Errorf("failed getting job in updateStatus %s with error: %w", jid, err)
	}

	if err = cbor.Unmarshal(getJob, &job); err != nil {
		return fmt.Errorf("failed unmarshalling job with error: %w", err)
	}

	job.Status = status

	if job.Status != commonIface.JobStatusCancelled {
		job.Logs = cid_log
		job.AssetCid = assetCid
		job.Attempt++
	}

	// TODO: Un-export job locks, and create methods
	jobData, err := cbor.Marshal(&job)
	if err != nil {
		return fmt.Errorf("marshal in updateStatus error: %w", err)
	}

	if job.Status == commonIface.JobStatusSuccess || job.Status == commonIface.JobStatusCancelled || job.Attempt >= servicesCommon.MaxJobAttempts {
		if err = p.db.Put(ctx, "/archive/jobs/"+jid, jobData); err != nil {
			return fmt.Errorf("updateStatus put failed with error: %w", err)
		}
	} else {
		if err = p.db.Put(ctx, "/jobs/"+jid, jobData); err != nil {
			return fmt.Errorf("updateStatus put failed with error: %w", err)
		}
	}

	if err = p.deleteJob(ctx, jid, "/locked/jobs/"); err != nil {
		return fmt.Errorf("failed delete in updateStatus with %w", err)
	}

	return nil
}

// Convert to string map helper
func convertToStringMap(_map interface{}) (map[string]string, error) {
	newMap := make(map[string]string, 0)
	stringMap, ok := _map.(map[interface{}]interface{})
	if !ok {
		return nil, fmt.Errorf("failed converting map to map[string][string]")
	}

	for key, value := range stringMap {
		strKey := fmt.Sprintf("%v", key)
		strValue := fmt.Sprintf("%v", value)
		newMap[strKey] = strValue
	}

	return newMap, nil
}

// Project struct for HTTP responses
type project struct {
	ProjectId string
	JobIds    []string
}

// HTTP Handlers

// Project all job handler
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

// Project job handler
func (srv *PatrickService) projectJobHandler(ctx http.Context) (iface interface{}, err error) {
	jid, err := maps.String(ctx.Variables(), "jid")
	if err != nil {
		return
	}

	var job *commonIface.Job
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

// Cancel job handler
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
			if err == nil {
				break
			}

			attempts++
			if attempts == servicesCommon.MaxCancelAttempts {
				return nil, fmt.Errorf("failed cancelling job %s max attempts exceeded", jid)
			}

			select {
			case <-requestCtx.Done():
				return nil, fmt.Errorf("cancelled while waiting for job %s to be locked: %w", jid, requestCtx.Err())
			case <-time.After(2 * time.Second):
			}
		}

	}

	var jobLock Lock
	err = cbor.Unmarshal(lockBytes, &jobLock)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshal job %s with %w", jid, err)
	}

	// Send directly to that monkey to cancel the job
	_, err = srv.monkeyClient.Peers(jobLock.Pid).Cancel(jid)
	if err != nil {
		return nil, fmt.Errorf("failed cancelling job %s on monkey with %w", jid, err)
	}

	return map[string]interface{}{"cancelled": jid}, nil
}

// Retry job handler
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

		if job.Status == commonIface.JobStatusSuccess {
			job.Status = commonIface.JobStatusFailed
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
	if job.Status == commonIface.JobStatusCancelled || job.Status == commonIface.JobStatusFailed || job.Status == commonIface.JobStatusSuccess {
		// Change to open and resend over pubsub
		job.Status = commonIface.JobStatusOpen
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

// Get job helper
func (srv *PatrickService) getJob(ctx context.Context, loc string, jid string) (*commonIface.Job, error) {
	jobByte, err := srv.db.Get(ctx, loc+jid)
	if err != nil {
		return nil, fmt.Errorf("get job %s failed with: %w", jid, err)
	}

	var job commonIface.Job
	err = cbor.Unmarshal(jobByte, &job)
	if err != nil {
		return nil, fmt.Errorf("unmarshal job %s failed with %w", jid, err)
	}

	return &job, nil
}

// CID handler
func (srv *PatrickService) cidHandler(ctx http.Context) (interface{}, error) {
	cid, err := ctx.GetStringVariable("cid")
	if err != nil {
		return nil, err
	}

	// use request context so there's no leak
	f, err := srv.node.GetFile(ctx.Request().Context(), cid)
	if err != nil {
		return nil, err
	}

	return http.RawStream{ContentType: "application/text", Stream: f}, nil
}

// Download asset handler
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

	contentType, err := filetype.Match(typeBuff)
	if err != nil {
		return nil, fmt.Errorf("failed filetype match for asset %s with %v", assetCid, err)
	}

	if _, err := file.Seek(0, 0); err != nil {
		return nil, fmt.Errorf("rewinding asset file %s failed with %s", assetCid, err)
	}

	fileData, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed reading asset file %s with %v", assetCid, err)
	}

	if contentType == matchers.TypeZip {
		return http.RawData{ContentType: "application/zip", Data: fileData}, nil
	} else {
		return http.RawData{ContentType: "application/wasm", Data: fileData}, nil
	}
}

// GitHub routes setup
func (srv *PatrickService) setupGithubRoutes() {
	var host string
	if !srv.devMode && len(srv.hostUrl) > 0 {
		host = "patrick.tau." + srv.hostUrl
	}

	srv.http.POST(&http.RouteDefinition{
		Host: host,
		Path: "/github/{hook}",
		Vars: http.Variables{
			Required: []string{"hook", "X-Hub-Signature", "X-Hub-Signature-256", "X-GitHub-Hook-ID"},
		},
		Scope: []string{"hook/push"},
		Auth: http.RouteAuthHandler{
			Validator: srv.githubCheckHookAndExtractSecret,
		},
		Handler: srv.githubHookHandler,
	})

	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/ping",
		Handler: func(ctx http.Context) (interface{}, error) {
			return map[string]string{"ping": "pong"}, nil
		},
	})
}

// Job routes setup
func (srv *PatrickService) setupJobRoutes() {
	var host string
	if !srv.devMode && len(srv.hostUrl) > 0 {
		host = "patrick.tau." + srv.hostUrl
	}

	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/jobs/{projectId}",
		Vars: http.Variables{
			Required: []string{"projectId"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.projectAllJobHandler,
	})

	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/job/{jid}",
		Vars: http.Variables{
			Required: []string{"jid"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.projectJobHandler,
	})

	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/download/{jobId}/{resourceId}",
		Vars: http.Variables{
			Required: []string{"jobId", "resourceId"},
		},
		Handler:     srv.downloadAsset,
		RawResponse: true,
	})

	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/logs/{cid}",
		Vars: http.Variables{
			Required: []string{"cid"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler:     srv.cidHandler,
		RawResponse: true,
	})

	srv.http.POST(&http.RouteDefinition{
		Host: host,
		Path: "/cancel/{jid}",
		Vars: http.Variables{
			Required: []string{"jid"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.cancelJob,
	})

	srv.http.POST(&http.RouteDefinition{
		Host: host,
		Path: "/retry/{jid}",
		Vars: http.Variables{
			Required: []string{"jid"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.retryJob,
	})

}

// GitHub token HTTP auth
func (srv *PatrickService) GitHubTokenHTTPAuth(ctx http.Context) (interface{}, error) {
	auth := httpAuth.GetAuthorization(ctx)
	if auth != nil && (auth.Type == "oauth" || auth.Type == "github") {
		rctx, rctx_cancel := context.WithTimeout(ctx.Request().Context(), time.Second*30)
		client, err := authService.NewGitHubClient(rctx, auth.Token)
		if err != nil {
			rctx_cancel()
			return nil, errors.New("invalid Github token")
		}
		ctx.SetVariable("GithubClient", client)
		ctx.SetVariable("GithubClientDone", rctx_cancel) // for GitHubTokenHTTPAuthCleanup to call so there's no leak
		logger.Debugf("[GitHubTokenHTTPAuth] ctx=%v", ctx.Variables())
		return nil, nil
	}
	return nil, errors.New("valid Github token required")
}

// GitHub token HTTP auth cleanup
func (srv *PatrickService) GitHubTokenHTTPAuthCleanup(ctx http.Context) (interface{}, error) {
	done, k := ctx.Variables()["GithubClientDone"]
	if k && done != nil {
		done.(context.CancelFunc)()
	}
	return nil, nil
}
