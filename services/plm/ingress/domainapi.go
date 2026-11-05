package ingress

import (
	"context"
	"net/http"

	"github.com/taubyte/tau/pkg/register"
)

// domainapi.go issues the TXT record a project's owner sets so the cloud believes
// a custom domain is the project's; a deploy declaring the zone is refused until
// it is found.

// DomainClaim is one TXT record to set.
type DomainClaim struct {
	Entry string `json:"entry"`
	Type  string `json:"type"`
	Token string `json:"token"`
}

// Domains signs a claim over (fqdn, project).
type Domains interface {
	Claim(ctx context.Context, project, fqdn string) (DomainClaim, error)
}

// handleClaimDomain is POST /domain/{fqdn}/for/{projectId}, for the project's
// owner: whoever decides what the project answers on.
func (s *Service) handleClaimDomain(w http.ResponseWriter, r *http.Request, token, projectID string) {
	fqdn, err := pathID(r, "fqdn")
	if err != nil {
		respond(w, err)
		return
	}
	if s.domains == nil {
		respond(w, errDenied)
		return
	}
	if _, err := s.mayTouchProject(r.Context(), token, projectID, register.RoleOwner); err != nil {
		respond(w, err)
		return
	}
	claim, err := s.domains.Claim(r.Context(), projectID, fqdn)
	if err != nil {
		respond(w, err)
		return
	}
	writeJSON(w, http.StatusOK, claim)
}
