package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
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
	"github.com/taubyte/tau/pkg/raft"
	servicesCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/utils/maps"

	http "github.com/taubyte/tau/pkg/http"
	httpAuth "github.com/taubyte/tau/pkg/http/auth"
	authService "github.com/taubyte/tau/services/auth"
)

func (srv *PatrickService) setupStreamRoutes() {
	srv.stream.Define("ping", func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		return cr.Response{"time": int(time.Now().Unix())}, nil
	})
	srv.stream.Define("patrick", srv.requestServiceHandler)
	srv.stream.Define("stats", srv.statsServiceHandler)
}

func (srv *PatrickService) setupHTTPRoutes() {
	srv.setupGithubRoutes()
	srv.setupJobRoutes()
}

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

	jid := ""
	if action != "list" && action != "dequeue" {
		jid, err = maps.String(body, "jid")
		if err != nil {
			return nil, fmt.Errorf("failed getting jid from body with error: %v", err)
		}
	}
	switch action {
	case "list":
		return p.listHandler(ctx)
	case "info":
		return p.infoHandler(ctx, jid)
	case "dequeue":
		return p.dequeueHandler(ctx, conn)
	case "isAssigned":
		return p.isAssignedHandler(ctx, jid, conn)
	case "refresh":
		return p.refreshHandler(ctx, jid, conn)
	case "cancel":
		return cr.Response{"cancelled": jid}, p.cancelHandler(ctx, jid, cidMap)
	case "done":
		return nil, p.doneHandler(ctx, jid, cidMap, assetMap, conn)
	case "failed":
		reason, _ := body["reason"].(string)
		return nil, p.failedHandler(ctx, jid, cidMap, assetMap, reason, conn)
	case "timeout":
		return nil, p.timeoutHandler(ctx, jid, cidMap)
	case "hasJob":
		return p.hasJobHandler(ctx, jid)
	}

	return nil, nil
}

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

func (p *PatrickService) hasJobHandler(ctx context.Context, jid string) (cr.Response, error) {
	if jid == "" {
		return cr.Response{"has": false}, nil
	}
	_, err := p.db.Get(ctx, "/jobs/"+jid)
	if err == nil {
		return cr.Response{"has": true}, nil
	}
	return cr.Response{"has": false}, nil
}

func (p *PatrickService) infoHandler(ctx context.Context, jid string) (cr.Response, error) {
	job, errFirst := p.getJob(ctx, "/jobs/", jid)
	if errFirst == nil {
		return cr.Response{"job": job}, nil
	}
	job, err := p.getJob(ctx, "/archive/jobs/", jid)
	if err == nil {
		return cr.Response{"job": job}, nil
	}
	if strings.Contains(errFirst.Error(), "unmarshal") {
		return nil, errFirst
	}
	if strings.Contains(err.Error(), "unmarshal") {
		return nil, err
	}
	return nil, fmt.Errorf("could not find %s in /archive/jobs or /jobs", jid)
}

// dequeueHandler pops the next job from the queue, records the assignment, and returns the job.
func (p *PatrickService) dequeueHandler(ctx context.Context, conn streams.Connection) (cr.Response, error) {
	// The job is already popped from the raft queue, so it must be fully
	// assigned or it is stranded (popped-but-unassigned) until ReAnnounce
	// re-homes it — a slow, avoidable round-trip. The pop is destructive, so
	// finish the read + durable assignment write on a detached, bounded context:
	// a request-ctx cancellation after the pop (e.g. the caller stream tearing
	// down) must not abandon the assignment.
	opCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()

	// Jobs of a project at its build cap are skipped and go back on the queue.
	var skipped []string
	defer func() {
		for _, id := range skipped {
			if err := p.enqueue(id); err != nil {
				logger.Errorf("requeue %s over its project's build cap: %s", id, err)
			}
		}
	}()

	quotas, capsOn := p.buildCaps()
	var running map[string]int
	for len(skipped) < maxCappedSkips {
		id, _, err := p.jobQueue.Pop(5 * time.Second)
		if err != nil {
			// A follower cannot pop, and removes nothing trying: it has no job to
			// give, which is an answer, not a failure. The monkey asks the next
			// Patrick, and the leader hands the job out.
			if errors.Is(err, raft.ErrQueueEmpty) || errors.Is(err, raft.ErrNotLeader) {
				return cr.Response{"available": false}, nil
			}
			return nil, fmt.Errorf("queue pop: %w", err)
		}

		job, err := p.getJob(opCtx, "/jobs/", id)
		if err != nil {
			return nil, fmt.Errorf("get job %s after pop: %w", id, err)
		}

		var project string
		if capsOn {
			project = p.projectOf(job)
			if running == nil {
				running = p.runningBuilds(opCtx, id)
			}
			if c := quotas.For(project).Builds; project != "" && c > 0 && running[project] >= c {
				skipped = append(skipped, id)
				continue
			}
		}

		assignData, err := cbor.Marshal(Assignment{
			MonkeyPID: conn.RemotePeer().String(),
			Timestamp: time.Now().Unix(),
			Project:   project,
		})
		if err != nil {
			return nil, fmt.Errorf("marshal assignment: %w", err)
		}
		if err := p.db.Put(opCtx, "/assigned/"+id, assignData); err != nil {
			return nil, fmt.Errorf("record assignment for %s: %w", id, err)
		}
		p.projects.Delete(id)
		return cr.Response{"available": true, "job": job}, nil
	}
	return cr.Response{"available": false}, nil
}

// callerAssignment returns the job's claim if the calling Monkey holds it, nil otherwise.
func (p *PatrickService) callerAssignment(ctx context.Context, jid string, conn streams.Connection) *Assignment {
	data, err := p.db.Get(ctx, "/assigned/"+jid)
	if err != nil {
		return nil
	}

	var assignment Assignment
	if err := cbor.Unmarshal(data, &assignment); err != nil || assignment.MonkeyPID != conn.RemotePeer().String() {
		return nil
	}

	return &assignment
}

// isAssignedHandler checks whether the calling Monkey is still the assigned owner of the job.
func (p *PatrickService) isAssignedHandler(ctx context.Context, jid string, conn streams.Connection) (cr.Response, error) {
	return cr.Response{"assigned": p.callerAssignment(ctx, jid, conn) != nil}, nil
}

// refreshHandler renews the calling Monkey's claim on the job, so re-announce
// does not hand it to another Monkey. Another Monkey's claim is left as is.
func (p *PatrickService) refreshHandler(ctx context.Context, jid string, conn streams.Connection) (cr.Response, error) {
	assignment := p.callerAssignment(ctx, jid, conn)
	if assignment == nil {
		return cr.Response{"assigned": false}, nil
	}

	assignment.Timestamp = time.Now().Unix()
	assignData, err := cbor.Marshal(assignment)
	if err != nil {
		return nil, fmt.Errorf("marshal assignment: %w", err)
	}

	if err := p.db.Put(ctx, "/assigned/"+jid, assignData); err != nil {
		return nil, fmt.Errorf("refresh assignment for %s: %w", jid, err)
	}

	return cr.Response{"assigned": true}, nil
}

func (p *PatrickService) timeoutHandler(ctx context.Context, jid string, cid_log map[string]string) error {
	if _, err := p.getJob(ctx, "/archive/jobs/", jid); err != nil {
		job, err := p.getJob(ctx, "/jobs/", jid)
		if err != nil {
			return fmt.Errorf("failed finding job %s in timeoutHandler with %v", jid, err)
		}

		if job.Attempt == servicesCommon.MaxJobAttempts {
			job.Status = commonIface.JobStatusFailed
			job.Logs = cid_log
			if err := p.recordLogOwner(ctx, jid, cid_log); err != nil {
				return fmt.Errorf("recording owners of job %s logs failed with %w", jid, err)
			}

			jobData, err := cbor.Marshal(job)
			if err != nil {
				return fmt.Errorf("marshal in updateStatus error: %w", err)
			}

			err = p.db.Put(ctx, "/archive/jobs/"+jid, jobData)
			if err != nil {
				return fmt.Errorf("failed put in timeoutHandler with %w", err)
			}

			err = p.deleteJob(ctx, jid, "/assigned/", "/jobs/")
			if err != nil {
				return fmt.Errorf("failed delete in timeoutHandler with %w", err)
			}

			return nil
		}

		job.Attempt++
		job.Timestamp = time.Now().Unix()
		job.Status = commonIface.JobStatusOpen

		if err = p.db.Delete(ctx, "/assigned/"+jid); err != nil {
			return fmt.Errorf("failed deleting assignment for %s: %w", jid, err)
		}

		job_bytes, err := cbor.Marshal(job)
		if err != nil {
			return fmt.Errorf("failed to marshal job %s with error: %w", jid, err)
		}

		if err = p.db.Put(ctx, "/jobs/"+jid, job_bytes); err != nil {
			return err
		}

		if err = p.enqueue(jid); err != nil {
			return fmt.Errorf("failed to push job %s back onto queue: %w", jid, err)
		}

		return nil
	}

	return fmt.Errorf("%s already finished", jid)
}

func (p *PatrickService) doneHandler(ctx context.Context, jid string, cid_log map[string]string, assetCid map[string]string, conn streams.Connection) error {
	return p.updateStatus(ctx, conn.RemotePeer(), jid, cid_log, commonIface.JobStatusSuccess, assetCid, "")
}

func (p *PatrickService) failedHandler(ctx context.Context, jid string, cid_log map[string]string, assetCid map[string]string, reason string, conn streams.Connection) error {
	return p.updateStatus(ctx, conn.RemotePeer(), jid, cid_log, commonIface.JobStatusFailed, assetCid, reason)
}

func (p *PatrickService) cancelHandler(ctx context.Context, jid string, cid_log map[string]string) error {
	return p.updateStatus(ctx, "", jid, cid_log, commonIface.JobStatusCancelled, nil, "")
}

func (p *PatrickService) deleteJob(ctx context.Context, jid string, loc ...string) error {
	for _, _loc := range loc {
		if err := p.db.Delete(ctx, _loc+jid); err != nil {
			return fmt.Errorf("failed deleting %s at %s with %v", jid, _loc, err)
		}
	}

	return nil
}

func (p *PatrickService) updateStatus(ctx context.Context, pid peer.ID, jid string, cid_log map[string]string, status commonIface.JobStatus, assetCid map[string]string, reason string) error {
	if pid != "" {
		data, err := p.db.Get(ctx, "/assigned/"+jid)
		if err == nil {
			var assignment Assignment
			if err := cbor.Unmarshal(data, &assignment); err == nil {
				if pid.String() != assignment.MonkeyPID {
					return fmt.Errorf("failed to update job %s, %s is not the owner", jid, pid)
				}
			}
		}
	}

	job, err := p.getJob(ctx, "/jobs/", jid)
	if err != nil {
		return fmt.Errorf("failed getting job in updateStatus %s with error: %w", jid, err)
	}

	job.Status = status

	if job.Status != commonIface.JobStatusCancelled {
		job.Logs = cid_log
		job.AssetCid = assetCid
		job.Reason = reason
		job.Attempt++
		if err := p.recordLogOwner(ctx, jid, cid_log); err != nil {
			return fmt.Errorf("recording owners of job %s logs failed with %w", jid, err)
		}
	}

	lastTry := reason != "" || job.Attempt >= servicesCommon.MaxJobAttempts

	// not the job's last try: it runs again, so it is open, as after a timeout
	if job.Status == commonIface.JobStatusFailed && !lastTry {
		job.Status = commonIface.JobStatusOpen
	}

	jobData, err := cbor.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal in updateStatus error: %w", err)
	}

	if job.Status == commonIface.JobStatusSuccess || job.Status == commonIface.JobStatusCancelled || lastTry {
		p.deleteJob(ctx, jid, "/jobs/")
		if err = p.db.Put(ctx, "/archive/jobs/"+jid, jobData); err != nil {
			return fmt.Errorf("updateStatus put failed with error: %w", err)
		}
	} else {
		if err = p.db.Put(ctx, "/jobs/"+jid, jobData); err != nil {
			return fmt.Errorf("updateStatus put failed with error: %w", err)
		}
	}

	if err = p.deleteJob(ctx, jid, "/assigned/"); err != nil {
		return fmt.Errorf("failed delete in updateStatus with %w", err)
	}

	return nil
}

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

type project struct {
	ProjectId string
	JobIds    []string
}

func (srv *PatrickService) projectAllJobHandler(ctx http.Context) (interface{}, error) {
	projectId, err := maps.String(ctx.Variables(), "projectId")
	if err != nil {
		return []string{}, err
	}

	prefix := fmt.Sprintf("/by/project/%s/", projectId)
	res, err := srv.db.List(ctx.Request().Context(), prefix)
	if err != nil {
		return []string{}, fmt.Errorf("get failed with: %w", err)
	}

	jids := make([]string, len(res))
	for idx, jobKey := range res {
		jids[idx] = strings.TrimPrefix(jobKey, prefix)
	}

	// ?last=N: the N most recent, newest first, so a client fetches only those
	if last, err := strconv.Atoi(ctx.Request().URL.Query().Get("last")); err == nil && last > 0 {
		jids = srv.newestJobs(ctx.Request().Context(), jids, last)
	}

	data := project{
		ProjectId: projectId,
		JobIds:    jids,
	}

	return data, nil
}

// newestJobs orders jobs newest first and keeps last of them. A job that cannot
// be read sorts as oldest.
func (srv *PatrickService) newestJobs(ctx context.Context, jids []string, last int) []string {
	stamps := make(map[string]int64, len(jids))
	for _, jid := range jids {
		if job, err := srv.anyJob(ctx, jid); err == nil {
			stamps[jid] = job.Timestamp
		}
	}
	sort.SliceStable(jids, func(i, j int) bool { return stamps[jids[i]] > stamps[jids[j]] })
	if len(jids) > last {
		jids = jids[:last]
	}
	return jids
}

func (srv *PatrickService) projectJobHandler(ctx http.Context) (iface interface{}, err error) {
	jid, err := maps.String(ctx.Variables(), "jid")
	if err != nil {
		return
	}

	var job *commonIface.Job
	requestCtx := ctx.Request().Context()

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

	_, err = srv.db.Get(requestCtx, "/archive/jobs/"+jid)
	if err == nil {
		return nil, fmt.Errorf("job %s already finished, cannot cancel", jid)
	}

	assignBytes, err := srv.db.Get(requestCtx, "/assigned/"+jid)
	if err != nil {
		_, err := srv.db.Get(requestCtx, "/jobs/"+jid)
		if err != nil {
			return nil, fmt.Errorf("job %s is not registered", jid)
		}

		var attempts int
		for {
			assignBytes, err = srv.db.Get(requestCtx, "/assigned/"+jid)
			if err == nil {
				break
			}

			attempts++
			if attempts == servicesCommon.MaxCancelAttempts {
				return nil, fmt.Errorf("failed cancelling job %s max attempts exceeded", jid)
			}

			select {
			case <-requestCtx.Done():
				return nil, fmt.Errorf("cancelled while waiting for job %s to be assigned: %w", jid, requestCtx.Err())
			case <-time.After(2 * time.Second):
			}
		}

	}

	var assignment Assignment
	err = cbor.Unmarshal(assignBytes, &assignment)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshal assignment for job %s with %w", jid, err)
	}

	monkeyPID, err := peer.Decode(assignment.MonkeyPID)
	if err != nil {
		return nil, fmt.Errorf("invalid monkey pid for job %s: %w", jid, err)
	}

	_, err = srv.monkeyClient.Peers(monkeyPID).Cancel(jid)
	if err != nil {
		return nil, fmt.Errorf("failed cancelling job %s on monkey with %w", jid, err)
	}

	return map[string]interface{}{"cancelled": jid}, nil
}

func (srv *PatrickService) retryJob(ctx http.Context) (iface interface{}, err error) {
	jid, err := ctx.GetStringVariable("jid")
	if err != nil {
		return nil, fmt.Errorf("failed finding map jid with %w", err)
	}

	requestCtx := ctx.Request().Context()

	_, err = srv.getJob(requestCtx, "/jobs/", jid)
	if err == nil {
		return map[string]interface{}{"retry": jid}, nil
	}

	if srv.devMode && servicesCommon.RetryJob {
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

	job, err := srv.getJob(requestCtx, "/archive/jobs/", jid)
	if err != nil {
		return nil, fmt.Errorf("failed grabbing archived job %s with %w", jid, err)
	}

	if job.Status == commonIface.JobStatusCancelled || job.Status == commonIface.JobStatusFailed || job.Status == commonIface.JobStatusSuccess {
		job.Status = commonIface.JobStatusOpen
		job.Attempt = 0
		job.Reason = ""
		job_byte, err := cbor.Marshal(job)
		if err != nil {
			logger.Errorf("failed cbor marshall on job %s with err: %v", job.Id, err)
			return nil, fmt.Errorf("failed marshalling job %s with err %w", job.Id, err)
		}

		err = srv.db.Delete(requestCtx, "/archive/jobs/"+jid)
		if err != nil {
			return nil, fmt.Errorf("failed deleting job %s with error: %w", jid, err)
		}

		// its next run writes a new log from the start
		if srv.liveLogs != nil {
			srv.liveLogs.reset(job.Id)
		}

		err = srv.db.Put(requestCtx, "/jobs/"+job.Id, job_byte)
		if err != nil {
			logger.Errorf("failed putting job %s into database with error: %s", job.Id, err.Error())
			return nil, fmt.Errorf("failed putting job %s with %w", job.Id, err)
		}

		if err = srv.enqueue(job.Id); err != nil {
			return nil, fmt.Errorf("failed to push retried job %s onto queue: %w", job.Id, err)
		}

		return map[string]interface{}{"retry": job.Id}, nil
	}

	return nil, errors.New("job is not in a state to be retried")
}

func (srv *PatrickService) getJob(ctx context.Context, loc string, jid string) (*commonIface.Job, error) {
	jobByte, err := srv.db.Get(ctx, loc+jid)
	if err != nil {
		return nil, fmt.Errorf("get job %s failed with: %w", jid, err)
	}

	var job commonIface.Job
	if err = job.Unmarshal(jobByte); err != nil {
		return nil, fmt.Errorf("unmarshal job %s failed with %w", jid, err)
	}

	return &job, nil
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
	jobId, err := ctx.GetStringVariable("jobId")
	if err != nil {
		return nil, fmt.Errorf("failed finding jobId with %w", err)
	}

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

func (srv *PatrickService) setupGithubRoutes() {
	hosts := srv.config.RouteHosts(servicesCommon.Patrick)
	srv.http.POST(&http.RouteDefinition{
		Hosts: hosts,
		Path:  "/github/{hook}",
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
		Hosts: hosts,
		Path:  "/ping",
		Handler: func(ctx http.Context) (interface{}, error) {
			return map[string]string{"ping": "pong"}, nil
		},
	})
}

func (srv *PatrickService) setupJobRoutes() {
	hosts := srv.config.RouteHosts(servicesCommon.Patrick)
	srv.http.GET(&http.RouteDefinition{
		Hosts: hosts,
		Path:  "/jobs/{projectId}",
		Vars: http.Variables{
			Required: []string{"projectId"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.withProjectAccess(srv.projectAllJobHandler),
	})

	srv.http.GET(&http.RouteDefinition{
		Hosts: hosts,
		Path:  "/usage/{projectId}",
		Vars: http.Variables{
			Required: []string{"projectId"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.withProjectAccess(srv.usageHandler),
	})

	srv.http.GET(&http.RouteDefinition{
		Hosts: hosts,
		Path:  "/job/{jid}",
		Vars: http.Variables{
			Required: []string{"jid"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.withJobAccess("jid", srv.projectJobHandler),
	})

	srv.http.GET(&http.RouteDefinition{
		Hosts: hosts,
		Path:  "/download/{jobId}/{resourceId}",
		Vars: http.Variables{
			Required: []string{"jobId", "resourceId"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler:     srv.withJobAccess("jobId", srv.downloadAsset),
		RawResponse: true,
	})

	srv.http.GET(&http.RouteDefinition{
		Hosts: hosts,
		Path:  "/logs/{cid}",
		Vars: http.Variables{
			Required: []string{"cid"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler:     srv.withLogAccess(srv.cidHandler),
		RawResponse: true,
	})

	srv.http.WebSocket(&http.WebSocketDefinition{
		Hosts: hosts,
		Path:  "/stream/logs/{jid}",
		Vars: http.Variables{
			Required: []string{"jid"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		NewHandler: srv.newLogStream,
	})

	srv.http.POST(&http.RouteDefinition{
		Hosts: hosts,
		Path:  "/cancel/{jid}",
		Vars: http.Variables{
			Required: []string{"jid"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.withJobAccess("jid", srv.cancelJob),
	})

	srv.http.POST(&http.RouteDefinition{
		Hosts: hosts,
		Path:  "/retry/{jid}",
		Vars: http.Variables{
			Required: []string{"jid"},
		},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.withJobAccess("jid", srv.retryJob),
	})

}

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
		ctx.SetVariable("GithubClientDone", rctx_cancel)
		logger.Debugf("[GitHubTokenHTTPAuth] %s", ctx.Request().URL.Path) // never the variables: they hold the token
		return nil, nil
	}
	return nil, errors.New("valid Github token required")
}

func (srv *PatrickService) GitHubTokenHTTPAuthCleanup(ctx http.Context) (interface{}, error) {
	done, k := ctx.Variables()["GithubClientDone"]
	if k && done != nil {
		done.(context.CancelFunc)()
	}
	return nil, nil
}
