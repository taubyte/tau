package frontend

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/register"
)

// buildapi.go is a build asked for by hand: `tau ci run`, and in a universe only,
// `tau build` uploading its result. A manual build is authorized as a builder on
// the project, then goes the way a delivered push does; the backend still confirms
// the ref at the forge. The commit is the caller's to name.

// maxBuildBody bounds the one small body a manual build reads.
const maxBuildBody = 4 << 10

// MaxUploadBytes bounds a universe's upload: a project's tree and what its
// builds produced.
const MaxUploadBytes = 512 << 20

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type buildRequest struct {
	Repository string `json:"repository_key"`
	Ref        string `json:"ref"`
	Commit     string `json:"commit_sha"`
}

type queued struct {
	JobID string `json:"job_id"`
}

// Uploads is a universe's own way in: a developer's tree and what was built
// from it, carried whole into a development cloud that then deploys it the way
// it deploys a push. NO CLOUD IS COMPOSED WITH ONE, so on a cloud the route
// does not exist and `tau build` publishes nothing.
type Uploads interface {
	// Put keeps a bundle as a commit of the project's configuration
	// repository, and answers the commit a queued build reads it at.
	Put(ctx context.Context, projectID, repositoryKey, ref string, bundle io.Reader) (commit string, err error)
}

// Published reads what a project's deployments currently run.
type Published interface {
	// Environments are the deployments published for project.
	Environments(ctx context.Context, project string) ([]string, error)
	// Compiled is the compiled object published for project's environment;
	// one never published is register.ErrNotLinked or a NotFound.
	Compiled(ctx context.Context, project, environment string) ([]byte, error)
}

// handleManualBuild is POST /build/{projectId}.
func (s *Service) handleManualBuild(w http.ResponseWriter, r *http.Request, token, projectID string) {
	var req buildRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBuildBody)).Decode(&req); err != nil {
		respond(w, problem(http.StatusBadRequest, "body must be {repository_key, ref, commit_sha}"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()

	if _, err := s.mayTouchProject(ctx, token, projectID, register.RoleBuilder); err != nil {
		respond(w, err)
		return
	}
	if _, ok := parseRepositoryKey(req.Repository); !ok {
		respond(w, problem(http.StatusBadRequest, "not a repository key: "+req.Repository))
		return
	}

	// THE REPOSITORY IS THE PROJECT'S, or this would be a builder on one
	// project queuing builds of another's.
	owner, err := s.projects.Project(ctx, req.Repository)
	switch {
	case err != nil && !errors.Is(err, register.ErrNotLinked) && connect.CodeOf(err) != connect.CodeNotFound:
		respond(w, err)
		return
	case err != nil || owner != projectID:
		respond(w, problem(http.StatusUnprocessableEntity, req.Repository+" is not linked to this project"))
		return
	}

	s.queue(w, ctx, Push{Repository: req.Repository, Ref: req.Ref, Commit: req.Commit})
}

// queue is a hand-made push from the point it is authorized: shape, the push
// policy every push meets, then Enqueue.
func (s *Service) queue(w http.ResponseWriter, ctx context.Context, push Push) {
	if !commitPattern.MatchString(push.Commit) {
		respond(w, problem(http.StatusBadRequest, "commit_sha must be a full 40-character commit"))
		return
	}

	// INELIGIBLE IS A REFUSAL HERE, not the 202 a webhook gets: a forge must
	// not retry a genuine push this cloud ignores, but a person asked for this
	// build and is owed the reason it will not run.
	if !s.eligible(push.Ref) {
		respond(w, problem(http.StatusUnprocessableEntity, push.Ref+" is not a ref this cloud builds"))
		return
	}
	if err := s.refusal(push); err != nil {
		respond(w, err)
		return
	}

	jobID, err := s.backend.Enqueue(ctx, push.Repository, push.Ref, push.Commit)
	if err != nil {
		// THE BACKEND'S REASON IS THE CALLER'S, for a refusal of the push
		// itself: a commit the forge does not hold at that ref is the one a
		// person most needs named, and a builder on the project may know it.
		if connect.CodeOf(err) == connect.CodeFailedPrecondition {
			var ce *connect.Error
			if errors.As(err, &ce) {
				respond(w, problem(http.StatusConflict, ce.Message()))
				return
			}
		}
		respond(w, err)
		return
	}
	s.log("queued job %s for %s %s by hand", jobID, push.Repository, push.Ref)
	writeJSON(w, http.StatusAccepted, queued{JobID: jobID})
}

// handleUpload is POST /upload/{projectId}?ref=…[&repository=…], mounted only
// where an Uploads was given. The repository is the project's configuration
// unless it names one of its code repositories: a code repository's build is
// uploaded as a commit of that repository, and built against what is published.
func (s *Service) handleUpload(w http.ResponseWriter, r *http.Request, token, projectID string) {
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()

	if _, err := s.mayTouchProject(ctx, token, projectID, register.RoleBuilder); err != nil {
		respond(w, err)
		return
	}
	record, err := s.linker.ProjectRecord(ctx, projectID)
	if err != nil {
		respond(w, err)
		return
	}

	repository := record.Config
	if named := r.URL.Query().Get("repository"); named != "" && named != record.Config {
		if !slices.Contains(record.Code, named) {
			respond(w, problem(http.StatusUnprocessableEntity, named+" is not linked to this project"))
			return
		}
		repository = named
	}

	ref := r.URL.Query().Get("ref")
	if !s.eligible(ref) {
		respond(w, problem(http.StatusUnprocessableEntity, ref+" is not a ref this cloud builds"))
		return
	}

	// THE BODY IS READ UNDER THE REQUEST'S OWN CONTEXT and not BackendTimeout,
	// which bounds one call out of this process: a bundle is as slow as the
	// developer's link.
	commit, err := s.uploads.Put(r.Context(), projectID, repository, ref, http.MaxBytesReader(w, r.Body, MaxUploadBytes))
	if err != nil {
		s.log("upload for %s: %v", projectID, err)
		respond(w, problem(http.StatusBadRequest, "the bundle could not be read: "+err.Error()))
		return
	}

	s.queue(w, ctx, Push{Repository: repository, Ref: ref, Commit: commit})
}

// handleDeployments is GET /projects/{projectId}/deployments: the
// environments a project has published.
func (s *Service) handleDeployments(w http.ResponseWriter, r *http.Request, token, projectID string) {
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()

	if _, err := s.mayTouchProject(ctx, token, projectID, register.RoleViewer); err != nil {
		respond(w, err)
		return
	}
	environments, err := s.published.Environments(ctx, projectID)
	if err != nil {
		respond(w, err)
		return
	}
	if environments == nil {
		environments = []string{}
	}
	writeJSON(w, http.StatusOK, environments)
}

// handlePublished is GET /projects/{projectId}/deployments/{environment}: the
// compiled object a deployment runs, which `tau build --published` builds
// against.
func (s *Service) handlePublished(w http.ResponseWriter, r *http.Request, token, projectID string) {
	environment, err := boundedPath(r, "environment", maxIDLength)
	if err != nil {
		respond(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()

	if _, err := s.mayTouchProject(ctx, token, projectID, register.RoleViewer); err != nil {
		respond(w, err)
		return
	}
	compiled, err := s.published.Compiled(ctx, projectID, environment)
	if err != nil {
		respond(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(compiled)
}
