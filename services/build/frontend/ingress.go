package frontend

import "net/http"

// ingress.go exists in exactly two packages in this tree — gateway's, when it
// is written, and this one — because exactly two services are publicly
// reachable (service-topology.md §2, service-conventions.md §5a). Its presence
// is half of that invariant; services/build/boundary_test.go asserts the other
// half by refusing the file a home in the backend.
//
// # It binds net/http's own ServeMux, and NOT pkg/http
//
// That is a deliberate break with service-conventions.md §5a, which says an
// IngressPublic service "binds pkg/http". It is stated loudly because it is a
// convention being broken rather than one overlooked, and there are two
// reasons, both measured rather than argued:
//
//  1. pkg/http RESOLVES A ROUTE VARIABLE FROM THE QUERY STRING FIRST.
//     pkg/http/context/helpers.go's `add` closure tries the query, then the mux
//     variables, then a header, then the body — so on every patrick route with
//     a path variable, `?hook=`, `?jid=` and `?projectId=` override the path.
//     On the webhook route that is the string which selects the HMAC key,
//     chosen by the caller twice over. net/http's PathValue reads the path and
//     nothing else, which ingress_test.go holds.
//  2. pkg/http installs no body cap, and no tau service sets one. The webhook
//     route reads a stranger's body before anything has been authenticated;
//     that read has to be bounded at the point of reading, which webhook.go
//     does with http.MaxBytesReader.
//
// Inheriting pkg/http would reintroduce both. When pkg/http is rewritten for
// v2 and resolves a path variable from the path, this file is where that
// choice is revisited.
//
// Go's own method-and-wildcard patterns give the other two properties for free,
// and both are asserted rather than assumed: a GET on a POST route is a 405
// rather than a route that quietly does not exist, and a {wildcard} spans
// exactly one segment, so /hook/a/b is a 404 rather than a hook id with a
// slash in it.
func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()

	// The one route a stranger reaches. Everything it does is in webhook.go, in
	// the order it does it.
	mux.HandleFunc("POST /hook/{projectId}", s.handleWebhook)

	// The four a forge-token holder reaches. Each authorizes on the resource
	// before it acts (authz.go); a denial is a 404.
	//
	// EVERY ONE OF THEM GOES THROUGH admitted(), and none of them could be
	// mounted here without it: a job handler is a jobHandler and not an
	// http.HandlerFunc, so the mux does not take one until admitted() has
	// wrapped it in the rate limits and the bounded path read (jobapi.go). A
	// fifth job route is admitted the day it compiles.
	mux.HandleFunc("GET /jobs/{projectId}", s.admitted("projectId", s.handleListJobs))
	mux.HandleFunc("GET /job/{jid}", s.admitted("jid", s.handleGetJob))
	mux.HandleFunc("POST /cancel/{jid}", s.admitted("jid", s.handleCancelJob))
	mux.HandleFunc("POST /retry/{jid}", s.admitted("jid", s.handleRetryJob))
	mux.HandleFunc("GET /job/{jid}/logs", s.admitted("jid", s.handleFollowJob))

	// The secret routes: written by a developer, out of band, because the value
	// is the one thing that must not be in git (secretapi.go). Same
	// authorization as everything above — write access to a repository the
	// project is made of — and no route that reads a value back.
	mux.HandleFunc("GET /secrets/{projectId}/{environment}", s.admitted("projectId", s.handleListSecrets))
	mux.HandleFunc("PUT /secrets/{projectId}/{environment}/{name}", s.admitted("projectId", s.handleSetSecret))
	mux.HandleFunc("DELETE /secrets/{projectId}/{environment}/{name}", s.admitted("projectId", s.handleDeleteSecret))

	// Linking a project: the one public door to the register's project table,
	// authorized as write access to EVERY repository it names, and the caller's
	// own list of what they linked (projectapi.go).
	mux.HandleFunc("POST /projects/{projectId}", s.admitted("projectId", s.handleLinkProject))
	mux.HandleFunc("GET /forges/{forge}/projects", s.admitted("forge", s.handleListProjects))

	// Signing in, and managing this cloud's users: auth's own calls, carried
	// to it unchanged and authorized by it (authapi.go). Only when this node
	// was given a way to reach auth.
	if s.authAPI != nil {
		for _, p := range authProcedures {
			mux.HandleFunc("POST "+p, s.handleAuth(p))
		}
	}

	// No /ping. v1 had one, unauthenticated, and a liveness endpoint is a
	// decision about what this process will tell the internet about itself
	// before anyone has asked for it — including that it is here. Nothing in
	// this cloud reads one.
	return mux
}
