package forgeauth

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/git/repokey"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/services/build/frontend"
)

// THE WHOLE ROUTE, WITH REAL SIGNATURES. The frontend's own tests fake what a
// delivery means; this one sends a genuinely signed delivery from each forge
// shape — each under its own scheme — through the real handler, and asserts
// what reaches the backend. It is the test that says the route is not GitHub's.

const commit = "1111111111111111111111111111111111111111"

type hooks struct{ hook *authv2.Hook }

func (h hooks) Get(context.Context, string) (*authv2.Hook, error) { return h.hook, nil }

type projects struct{}

func (projects) Repositories(context.Context, string) ([]string, error) { return nil, nil }

// backend records what was queued. Everything but Enqueue is unreached here.
type backend struct {
	frontend.Backend
	mu     sync.Mutex
	queued []frontend.Push
}

func (b *backend) Enqueue(_ context.Context, key, ref, sha string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.queued = append(b.queued, frontend.Push{Repository: key, Ref: ref, Commit: sha})
	return "job", nil
}

func TestEveryForgesDeliveriesReachTheSameRoute(t *testing.T) {
	for _, shape := range []fake.Shape{fake.GitHubLike, fake.GitLabLike, fake.GiteaLike, fake.BitbucketLike} {
		t.Run(shape.Host, func(t *testing.T) {
			f := fake.New(shape)
			reg, err := f.Registry(forge.Policy{})
			require.NoError(t, err)
			forges, err := New(reg)
			require.NoError(t, err)

			secret, err := forge.NewSecret()
			require.NoError(t, err)
			sig, ok := f.Driver().(forge.Signatures)
			require.True(t, ok)
			key := f.Keys().Writable
			hook := forge.Hook{Key: key, Handle: "7", Scheme: sig.Scheme(), Secret: secret}

			queue := &backend{}
			svc, err := frontend.New(frontend.Config{},
				frontend.WithHooks(hooks{&authv2.Hook{
					Id: "hook-1", RepositoryKey: key.String(), Handle: "7",
					Scheme: string(sig.Scheme()), Secret: secret.Expose(),
				}}),
				frontend.WithDeliveries(forges),
				frontend.WithIdentity(forges),
				frontend.WithProjects(projects{}),
				frontend.WithBackend(queue))
			require.NoError(t, err)

			post := func(d forge.Delivery, tamper bool) *httptest.ResponseRecorder {
				body := d.Body
				if tamper {
					body = bytes.Replace(body, []byte(commit), []byte(strings.Repeat("2", 40)), 1)
				}
				r := httptest.NewRequest(http.MethodPost, "/hook/hook-1", bytes.NewReader(body))
				r.Header = d.Header.Clone()
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				svc.Handler().ServeHTTP(w, r)
				return w
			}

			// a genuine push is queued, for the hook's own repository
			push, err := f.Delivery(hook, "refs/heads/main", commit)
			require.NoError(t, err)
			require.Equal(t, http.StatusAccepted, post(push, false).Code)
			require.Equal(t, []frontend.Push{{Repository: key.String(), Ref: "refs/heads/main", Commit: commit}}, queue.queued)

			// the same bytes changed after signing do not verify
			require.Equal(t, http.StatusForbidden, post(push, true).Code)

			// a genuine signature speaking for another repository does not verify
			other, err := repokey.Parse("https://" + shape.Host + "/someone/else")
			require.NoError(t, err)
			foreign, err := f.DeliveryFor(hook, other, "refs/heads/main", commit)
			require.NoError(t, err)
			require.Equal(t, http.StatusForbidden, post(foreign, false).Code)

			// a genuine ping is accepted and costs no build
			ping, err := f.NonPushDelivery(hook)
			require.NoError(t, err)
			w := post(ping, false)
			require.Equal(t, http.StatusAccepted, w.Code)
			require.Equal(t, "not a push event", w.Header().Get("X-Build-Ignored"))

			// a genuine deletion is accepted and costs no build
			gone, err := f.Delivery(hook, "refs/heads/main", strings.Repeat("0", 40))
			require.NoError(t, err)
			w = post(gone, false)
			require.Equal(t, http.StatusAccepted, w.Code)
			require.Equal(t, "ref deleted", w.Header().Get("X-Build-Ignored"))

			require.Len(t, queue.queued, 1, "only the genuine push was queued")
		})
	}
}

// A HOOK THIS CLOUD CANNOT CHECK is not a delivery that failed: the forge was
// never asked, and saying 403 would blame the sender for the cloud's config.
func TestAHookThisCloudCannotCheckIsNotAnUnverifiedDelivery(t *testing.T) {
	reg, err := fake.New(fake.GitHubLike).Registry(forge.Policy{})
	require.NoError(t, err)
	forges, err := New(reg)
	require.NoError(t, err)

	_, err = forges.Verify(&authv2.Hook{RepositoryKey: "codeberg.org/acme/pay", Scheme: "hub-sha256", Secret: bytes.Repeat([]byte("k"), 32)}, http.Header{}, []byte("{}"))
	require.Error(t, err)
	require.NotErrorIs(t, err, frontend.ErrUnverified)

	_, err = forges.Verify(&authv2.Hook{RepositoryKey: "not a key"}, http.Header{}, nil)
	require.ErrorContains(t, err, "names no repository")
}

// A GENUINE DELIVERY NAMING NO USABLE PUSH is the forge's malformed payload,
// not an attack and not the cloud's config.
func TestAGenuineButUnreadableDeliveryIsMalformed(t *testing.T) {
	f := fake.New(fake.GitHubLike)
	reg, err := f.Registry(forge.Policy{})
	require.NoError(t, err)
	forges, err := New(reg)
	require.NoError(t, err)

	secret, err := forge.NewSecret()
	require.NoError(t, err)
	body := []byte("{not json")
	header, err := forge.Sign(forge.SchemeHubSHA256, secret, body, "delivery-1", time.Now())
	require.NoError(t, err)
	header.Set("X-Fake-Event", "push")

	_, err = forges.Verify(&authv2.Hook{
		RepositoryKey: f.Keys().Writable.String(), Scheme: string(forge.SchemeHubSHA256), Secret: secret.Expose(),
	}, header, body)
	require.ErrorIs(t, err, frontend.ErrMalformed)
}
