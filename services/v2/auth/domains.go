package auth

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/ipfs/go-cid"
	dv "github.com/taubyte/domain-validation"

	authv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/auth/v1"
	"github.com/taubyte/tau/pkg/v2/proto/gen/tau/auth/v1/authv1connect"
)

// domainHandler implements authv1connect.DomainServiceHandler. See
// repositories.go's repositoryHandler doc for why this is its own type
// rather than a method set on *Service.
type domainHandler struct{ s *Service }

var _ authv1connect.DomainServiceHandler = (*domainHandler)(nil)

// minProjectIDLength mirrors services/auth/api_hooks.go's registerDomainStream,
// which refuses a project id shorter than 8 characters before it even tries
// to cid.Decode it.
//
// IT IS LOAD-BEARING, and cid.Decode does not subsume it. The DNS entry
// this RPC answers with is `projectID[:minProjectIDLength]`, so a project id
// shorter than that is an out-of-range slice — a panic, in a handler any
// authenticated peer can reach. And short CIDs are real, not hypothetical:
// an identity multihash of nothing, CIDv1, base58btc, is the six-character
// "z2yYDV", which cid.Decode accepts. Removing this guard therefore does not
// merely lose a nicer error message, it hands anyone a way to kill the
// serving goroutine — which is why domains_test.go feeds it exactly that
// string rather than only a short thing that was never a CID anyway.
const minProjectIDLength = 8

// Register signs a domain-ownership claim for fqdn against projectID. It
// does not check that projectID names a project that exists — v1's
// registerDomainStream does not either — because that verification happens
// on the read side, when whoever resolves the DNS record checks the claim
// against the project's own published config, not here at signing time.
func (h *domainHandler) Register(ctx context.Context, req *connect.Request[authv1.RegisterDomainRequest]) (*connect.Response[authv1.RegisterDomainResponse], error) {
	fqdn := req.Msg.GetFqdn()
	if err := requireID("fqdn", fqdn); err != nil {
		return nil, err
	}

	projectID := req.Msg.GetProjectId()
	if len(projectID) < minProjectIDLength {
		return nil, errInvalidArgument("project_id is too short")
	}

	project, err := cid.Decode(projectID)
	if err != nil {
		return nil, errInvalidArgument("project_id: %w", err)
	}

	claim, err := dv.New(dv.FQDN(fqdn), dv.Project(project), dv.PrivateKey([]byte(h.s.cfg.DomainValidation.Private)), dv.PublicKey([]byte(h.s.cfg.DomainValidation.Public)))
	if err != nil {
		return nil, errInternal("building domain claim: %w", err)
	}

	token, err := claim.Sign()
	if err != nil {
		return nil, errInternal("signing domain claim: %w", err)
	}

	return connect.NewResponse(&authv1.RegisterDomainResponse{
		Token:         string(token),
		DnsEntry:      fmt.Sprintf("%s.%s", projectID[:minProjectIDLength], fqdn),
		DnsRecordType: "txt",
	}), nil
}
