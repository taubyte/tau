package auth

import (
	"errors"

	authIface "github.com/taubyte/tau/core/services/auth"
	"github.com/taubyte/tau/pkg/git/repokey"
	http "github.com/taubyte/tau/pkg/http"
)

// ErrProjectNotFound is what a caller is told when they may not see a project,
// whether the project is missing or simply not theirs. The two answers are
// deliberately the same string: telling a caller a project exists but isn't
// theirs turns any route that takes a project id back into the id oracle these
// checks exist to close.
var ErrProjectNotFound = errors.New("project not found")

// AuthorizeRepositories is the project-access rule, and the only one: a caller
// is authorized for a project when they can write to at least one of the
// repositories that project is linked to. Read access is not enough — a public
// repository reads to everyone.
//
// It takes the repository spellings rather than a project because the services
// that need it hold the project in different shapes: this service reads it out
// of its own store, seer and patrick get it from the auth client over p2p. The
// rule is the same for all of them, so it lives here once.
func AuthorizeRepositories(client GitHubClient, repositories ...string) error {
	if client == nil {
		return ErrProjectNotFound
	}

	for _, spelling := range repositories {
		key, err := repokey.Parse("https://" + spelling)
		if err != nil {
			continue
		}
		if repoAccess(client, key) {
			return nil
		}
	}

	return ErrProjectNotFound
}

// AuthorizeProject answers, for a caller this service has already
// authenticated, whether they may see project projectID.
//
// A token says who is asking; it never says what they may see. Every route that
// serves something a project owns — its configuration, its build assets, its
// jobs — has to ask this second question about the object named in the path,
// and this is the one place that answers it. The GitHub client is the one
// GitHubTokenHTTPAuth built from the caller's own token, so the write access
// checked is the caller's, not the node's.
func AuthorizeProject(ctx http.Context, authClient authIface.Client, projectID string) error {
	if authClient == nil {
		return errors.New("no auth client to resolve project with")
	}

	client, err := getGithubClientFromContext(ctx)
	if err != nil {
		return err
	}

	project := authClient.Projects().Get(projectID)
	if project == nil {
		return ErrProjectNotFound
	}

	repositories := make([]string, 0, 2)
	if project.Git.Config != nil {
		repositories = append(repositories, project.Git.Config.Id())
	}
	if project.Git.Code != nil {
		repositories = append(repositories, project.Git.Code.Id())
	}

	return AuthorizeRepositories(client, repositories...)
}

// RouteAuth builds the whole auth block a route that serves something a project
// owns declares: the token check and the object check, each in its own field of
// the route table. A token says who is asking and never what they may see, so
// both halves are named where the route is registered — a route carrying only
// the first is visibly missing the second rather than merely behaving as if it
// were absent.
//
// The order and the release-on-refusal are the router's, in
// http.RouteAuthHandler.Validate.
func RouteAuth(authenticate, cleanup http.Handler, object func(http.Context) error) http.RouteAuthHandler {
	return http.RouteAuthHandler{
		Validator: authenticate,
		Object:    object,
		GC:        cleanup,
	}
}
