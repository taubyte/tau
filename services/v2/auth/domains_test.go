package auth

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/taubyte/tau/pkg/v2/proto/gen/auth/v1"
)

// testProjectID is a valid CIDv0-shaped id — the same shape utils/id.Generate
// produces (store.go's generateDeployKey doc; projects.go's Create uses the
// same package) — long enough to satisfy minProjectIDLength.
func testProjectID(t testing.TB) string {
	t.Helper()
	c, err := cid.V0Builder{}.Sum([]byte("a project"))
	require.NoError(t, err)
	return c.String()
}

func TestDomainRegister(t *testing.T) {
	s := newTestService(t)
	h := &domainHandler{s}
	projectID := testProjectID(t)

	res, err := h.Register(t.Context(), connect.NewRequest(&authv1.RegisterDomainRequest{
		Fqdn:      "www.example.com",
		ProjectId: projectID,
	}))
	require.NoError(t, err)
	assert.NotEmpty(t, res.Msg.GetToken())
	assert.Equal(t, projectID[:minProjectIDLength]+".www.example.com", res.Msg.GetDnsEntry())
	assert.Equal(t, "txt", res.Msg.GetDnsRecordType())
}

func TestDomainRegister_EmptyFQDN(t *testing.T) {
	s := newTestService(t)
	h := &domainHandler{s}

	_, err := h.Register(t.Context(), connect.NewRequest(&authv1.RegisterDomainRequest{
		Fqdn:      "",
		ProjectId: testProjectID(t),
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestDomainRegister_ShortProjectID(t *testing.T) {
	s := newTestService(t)
	h := &domainHandler{s}

	_, err := h.Register(t.Context(), connect.NewRequest(&authv1.RegisterDomainRequest{
		Fqdn:      "www.example.com",
		ProjectId: "short",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestDomainRegister_NotACID(t *testing.T) {
	s := newTestService(t)
	h := &domainHandler{s}

	_, err := h.Register(t.Context(), connect.NewRequest(&authv1.RegisterDomainRequest{
		Fqdn:      "www.example.com",
		ProjectId: "not-a-cid-but-long-enough",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// TestDomainRegister_UnparsablePrivateKey reaches dv.New's own failure —
// New only checks the domain-validation keys are non-empty (service.go),
// not that they parse as PEM, so a Service built with well-formed-but-wrong
// bytes still reaches Register, which is where github.com/taubyte/domain-validation
// actually parses them.
func TestDomainRegister_UnparsablePrivateKey(t *testing.T) {
	s := &Service{cfg: Config{DomainValidation: KeyPair{Private: "not a pem", Public: "not a pem"}}}
	h := &domainHandler{s}

	_, err := h.Register(t.Context(), connect.NewRequest(&authv1.RegisterDomainRequest{
		Fqdn:      "www.example.com",
		ProjectId: testProjectID(t),
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

// TestDomainRegister_TooShortAProjectID is the guard minProjectIDLength
// actually exists for, and the reason TestDomainRegister_ShortProjectID
// above proves nothing on its own: "short" is refused by cid.Decode too, so
// deleting the length check leaves that test passing.
//
// These are not junk. Each is a real CID — CIDv1 over an identity multihash,
// base58btc or base32 — and cid.Decode accepts every one. Anything shorter
// than minProjectIDLength reaches `projectID[:minProjectIDLength]`, which is
// a slice out of range: a panic in a handler any authenticated peer can
// reach. The eight-character case is the other side of the same boundary,
// so the number cannot drift by one in either direction unnoticed.
func TestDomainRegister_TooShortAProjectID(t *testing.T) {
	for _, tc := range []struct {
		projectID string
		accepted  bool
	}{
		{"z2yYDV", false},   // 6
		{"z9iYnB1", false},  // 7 — minProjectIDLength - 1
		{"bafkqaaa", true},  // 8 — exactly minProjectIDLength
		{"bafkqaaia", true}, // 9
	} {
		t.Run(tc.projectID, func(t *testing.T) {
			_, err := cid.Decode(tc.projectID)
			require.NoError(t, err, "the fixture is only interesting if it really is a valid CID")

			s := newTestService(t)
			h := &domainHandler{s}
			res, err := h.Register(t.Context(), connect.NewRequest(&authv1.RegisterDomainRequest{
				Fqdn:      "www.example.com",
				ProjectId: tc.projectID,
			}))
			if !tc.accepted {
				require.Error(t, err)
				assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.projectID[:minProjectIDLength]+".www.example.com", res.Msg.GetDnsEntry())
		})
	}
}
