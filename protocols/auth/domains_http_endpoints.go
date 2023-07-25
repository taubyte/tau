package auth

import (
	_ "embed"
	"errors"
	"fmt"

	"github.com/ipfs/go-cid"
	dv "github.com/taubyte/domain-validation"
	http "github.com/taubyte/http"
)

func (srv *AuthService) tokenDomainHTTPHandler(ctx http.Context) (interface{}, error) {
	fqdn, err := ctx.GetStringVariable("fqdn")
	if err != nil {
		return nil, err
	}

	_project, err := ctx.GetStringVariable("project")
	if err != nil {
		return nil, err
	}

	if len(_project) < 8 {
		return nil, errors.New("project is too short")
	}

	project, err := cid.Decode(_project)
	if err != nil {
		return nil, fmt.Errorf("decode project id  failed with %w", err)
	}

	var claim *dv.Claims

	claim, err = domainValidationNew(fqdn, project, srv.dvPrivateKey, srv.dvPublicKey)
	if err != nil {
		return nil, fmt.Errorf("new domain validation failed with: %s", err)
	}

	fmt.Printf("CLAIMS::::: %#v", claim)

	token, err := claim.Sign()
	if err != nil {
		return nil, fmt.Errorf("signing claim failed with: %s", err)
	}

	return map[string]string{
		"token": string(token),
		"entry": fmt.Sprintf("%s.%s", _project[:8], fqdn),
		"type":  "txt",
	}, nil
}

func (srv *AuthService) setupDomainsHTTPRoutes() {
	host := ""
	if len(srv.hostUrl) > 0 {
		host = "auth.tau." + srv.hostUrl
	}

	srv.http.POST(&http.RouteDefinition{
		Host: host,
		Path: "/domain/{fqdn}/for/{project}",
		Vars: http.Variables{
			Required: []string{"project", "fqdn"},
		},
		Scope: []string{"/domain"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.tokenDomainHTTPHandler,
	})

}
