package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/core/services/patrick"
	tauConfig "github.com/taubyte/tau/pkg/config"
	http "github.com/taubyte/tau/pkg/http"
	servicesCommon "github.com/taubyte/tau/services/common"
)

// maxCappedSkips bounds the capped jobs one dequeue pops past, so a long queue
// of one capped project costs a poll a few raft writes, not the whole queue.
const maxCappedSkips = 16

// buildCaps returns the quotas and whether any build cap is set.
func (p *PatrickService) buildCaps() (tauConfig.Quotas, bool) {
	if p.config == nil {
		return tauConfig.Quotas{}, false
	}
	q := p.config.Quotas()
	if q.Default.Builds != 0 {
		return q, true
	}
	for _, c := range q.Projects {
		if c.Builds != 0 {
			return q, true
		}
	}
	return q, false
}

// projectOf returns the job's project, looked up once per job; "" if unknown (never capped).
func (p *PatrickService) projectOf(job *patrick.Job) string {
	if v, ok := p.projects.Load(job.Id); ok {
		return v.(string)
	}
	project, err := p.getProjectIDFromJob(job)
	if err != nil {
		logger.Warnf("job %s: project lookup for its build cap failed, not capping it: %s", job.Id, err)
		return ""
	}
	p.projects.Store(job.Id, project)
	return project
}

// usageHandler answers a project's running builds and its caps. Running
// builds are the project's jobs with a claim in /assigned, so the answer needs
// no project lookup and holds with build caps off.
// shortcut: builds only; instance and memory use live on each substrate node, add them with a node operator endpoint.
func (srv *PatrickService) usageHandler(ctx http.Context) (interface{}, error) {
	projectID, err := ctx.GetStringVariable("projectId")
	if err != nil {
		return nil, err
	}
	rctx := ctx.Request().Context()
	prefix := "/by/project/" + projectID + "/"
	jobs, err := srv.db.List(rctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("listing the project's jobs: %w", err)
	}
	assigned, err := srv.db.List(rctx, "/assigned/")
	if err != nil {
		return nil, fmt.Errorf("listing running builds: %w", err)
	}
	ofProject := make(map[string]bool, len(jobs))
	for _, key := range jobs {
		ofProject[strings.TrimPrefix(key, prefix)] = true
	}
	running := 0
	for _, key := range assigned {
		if ofProject[strings.TrimPrefix(key, "/assigned/")] {
			running++
		}
	}
	quotas, _ := srv.buildCaps()
	return map[string]any{"project": projectID, "builds_running": running, "caps": quotas.For(projectID)}, nil
}

// runningBuilds counts live claims per project from the replicated /assigned
// state, so every Patrick and every new leader sees the same counts. A claim
// past its lock time (dead monkey) and the claim of the job being handed out
// (re-announced) hold no slot.
func (p *PatrickService) runningBuilds(ctx context.Context, jid string) map[string]int {
	running := map[string]int{}
	keys, err := p.db.List(ctx, "/assigned/")
	if err != nil {
		logger.Warnf("listing running builds for the build cap: %s", err)
		return running
	}
	expired := time.Now().Unix() - int64(servicesCommon.DefaultLockTime.Seconds())
	for _, key := range keys {
		if strings.TrimPrefix(key, "/assigned/") == jid {
			continue
		}
		data, err := p.db.Get(ctx, key)
		if err != nil {
			continue
		}
		var a Assignment
		if cbor.Unmarshal(data, &a) == nil && a.Project != "" && a.Timestamp > expired {
			running[a.Project]++
		}
	}
	return running
}
