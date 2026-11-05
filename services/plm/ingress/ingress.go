package ingress

import "net/http"

// mount builds the route table. ingress.go exists only in the two publicly
// reachable services; boundary_test.go refuses it in jobs.
//
// It binds net/http's ServeMux, not pkg/http, on purpose: pkg/http resolves a
// route variable from the query string before the path (so `?hook=` would pick
// the HMAC key), and installs no body cap. PathValue reads only the path
// (ingress_test.go), a GET on a POST route is a 405, and a {wildcard} spans one
// segment.
func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()

	// The one route a stranger reaches. Everything it does is in webhook.go, in
	// the order it does it.
	mux.HandleFunc("POST /hook/{projectId}", s.handleWebhook)

	// The job routes, each authorizing the caller's role before acting. Every one goes
	// through admitted(): a jobHandler is not an http.HandlerFunc, so it cannot be
	// mounted unwrapped.
	mux.HandleFunc("GET /jobs/{projectId}", s.admitted("projectId", s.handleListJobs))
	mux.HandleFunc("GET /job/{jid}", s.admitted("jid", s.handleGetJob))
	mux.HandleFunc("POST /cancel/{jid}", s.admitted("jid", s.handleCancelJob))
	mux.HandleFunc("POST /retry/{jid}", s.admitted("jid", s.handleRetryJob))
	mux.HandleFunc("GET /job/{jid}/logs", s.admitted("jid", s.handleFollowJob))
	mux.HandleFunc("GET /cloud", s.handleCloud)
	mux.HandleFunc("GET /job/{jid}/logs/{key}", s.admitted("jid", s.handleJobLog))
	mux.HandleFunc("GET /job/{jid}/assets/{resource}", s.admitted("jid", s.handleJobAsset))

	// A build somebody asked for by hand (buildapi.go): admitted, authorized
	// at the builder role, and queued exactly as a verified push is.
	mux.HandleFunc("POST /build/{projectId}", s.admitted("projectId", s.handleManualBuild))
	if s.published != nil {
		mux.HandleFunc("GET /projects/{projectId}/deployments", s.admitted("projectId", s.handleDeployments))
		mux.HandleFunc("GET /projects/{projectId}/deployments/{environment}", s.admitted("projectId", s.handlePublished))
	}

	// A DEVELOPMENT CLOUD'S OWN DOOR, and only one configured with an uploads
	// directory has it: on a cloud nothing accepts a build's output from outside.
	if s.uploads != nil {
		mux.HandleFunc("POST /upload/{projectId}", s.admitted("projectId", s.handleUpload))
	}

	// The secret routes, written out of band because the value must not be in git
	// (secretapi.go), at the secret-manager role, with no read-back route.
	mux.HandleFunc("GET /secrets/{projectId}/{environment}", s.admitted("projectId", s.handleListSecrets))
	mux.HandleFunc("PUT /secrets/{projectId}/{environment}/{name}", s.admitted("projectId", s.handleSetSecret))
	mux.HandleFunc("DELETE /secrets/{projectId}/{environment}/{name}", s.admitted("projectId", s.handleDeleteSecret))

	// The TXT record that proves a custom domain is the project's (domainapi.go).
	mux.HandleFunc("POST /domain/{fqdn}/for/{projectId}", s.admitted("projectId", s.handleClaimDomain))

	// Linking a project, the caller's own projects, and an owner's grants:
	// the one public door to the register's project table (projectapi.go,
	// grantapi.go).
	mux.HandleFunc("POST /projects/{projectId}", s.admitted("projectId", s.handleLinkProject))
	mux.HandleFunc("GET /projects/{projectId}", s.admitted("projectId", s.handleGetProject))
	mux.HandleFunc("GET /projects", s.admitted("", s.handleListProjects))
	mux.HandleFunc("GET /projects/{projectId}/grants", s.admitted("projectId", s.handleListGrants))
	mux.HandleFunc("POST /projects/{projectId}/grants", s.admitted("projectId", s.handleGrant))
	mux.HandleFunc("DELETE /projects/{projectId}/grants/{grantId}", s.admitted("projectId", s.handleRevoke))

	// Signing in, and managing this cloud's users: auth's own calls, carried
	// to it unchanged and authorized by it (authapi.go). Only when this node
	// was given a way to reach auth.
	if s.authAPI != nil {
		for _, p := range authProcedures {
			mux.HandleFunc("POST "+p, s.handleAuth(p))
		}
	}

	// No /ping: nothing reads one, and this process tells the internet nothing
	// unasked.
	return mux
}
