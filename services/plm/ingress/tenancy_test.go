package ingress

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/tenancy"
)

func servesOnly(t *testing.T, namespaces ...string) Option {
	t.Helper()
	ten, err := tenancy.Parse(namespaces)
	require.NoError(t, err)
	return WithTenancy(ten)
}

func TestAPushOutsideTheTenancyIsNotBuilt(t *testing.T) {
	h := newHarness(t, Config{}, servesOnly(t, "github.com/someone-else"))
	w := h.do(t, webhookRequest(webhookOpts{}))
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), testRepoKey+" is outside this cloud's tenancy; it serves github.com/someone-else")
	require.Empty(t, h.jobs.enqueues())

	// A verified push naming no key a tenancy can place is refused too.
	h = newHarness(t, Config{}, servesOnly(t, "github.com/someone-else"))
	h.deliveries.verify = func(Hook, http.Header, []byte) (Push, error) {
		return Push{Repository: "GitHub.com/x/y", Ref: "refs/heads/main", Commit: testSHA}, nil
	}
	w = h.do(t, webhookRequest(webhookOpts{}))
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Empty(t, h.jobs.enqueues())
}

func TestAPushInsideTheTenancyIsBuilt(t *testing.T) {
	h := newHarness(t, Config{}, servesOnly(t, "GitHub.com/Acme"))
	require.Equal(t, http.StatusAccepted, h.do(t, webhookRequest(webhookOpts{})).Code)
	require.Len(t, h.jobs.enqueues(), 1)
}
