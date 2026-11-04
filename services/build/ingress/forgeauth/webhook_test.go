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
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/build/ingress"
)

// The whole route with real signatures from each forge shape, through the real
// handler.

const commit = "1111111111111111111111111111111111111111"

type hooks struct{ hook ingress.Hook }

func (h hooks) Hook(context.Context, string) (ingress.Hook, error) { return h.hook, nil }

type projects struct{}

func (projects) Role(context.Context, string, string) (register.Role, error) {
	return register.RoleNone, nil
}
func (projects) Project(context.Context, string) (string, error) { return "", nil }

type callers struct{}

func (callers) Caller(context.Context, string) (string, error) { return "", ingress.ErrBadCredential }

// backend records what was queued. Everything but Enqueue is unreached here.
type backend struct {
	ingress.Backend
	mu     sync.Mutex
	queued []ingress.Push
}

func (b *backend) Enqueue(_ context.Context, key, ref, sha string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.queued = append(b.queued, ingress.Push{Repository: key, Ref: ref, Commit: sha})
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
			adminSecret, err := forge.NewSecret()
			require.NoError(t, err)
			adminHook := forge.Hook{Key: f.Keys().Admin, Handle: "8", Scheme: sig.Scheme(), Secret: adminSecret}

			queue := &backend{}
			svc, err := ingress.New(ingress.Config{},
				// TWO REPOSITORIES, each under its own secret, the pushed one
				// second, so the delivery is verified by trying them in turn.
				ingress.WithHooks(hooks{ingress.Hook{
					Project: "hook-1", Repositories: []ingress.HookRepository{
						{Key: f.Keys().Admin.String(), Secret: adminSecret.Expose()},
						{Key: key.String(), Secret: secret.Expose()},
					},
				}}),
				ingress.WithDeliveries(forges),
				ingress.WithCallers(callers{}),
				ingress.WithProjects(projects{}),
				ingress.WithBackend(queue))
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
			require.Equal(t, []ingress.Push{{Repository: key.String(), Ref: "refs/heads/main", Commit: commit}}, queue.queued)

			// the same bytes changed after signing do not verify
			require.Equal(t, http.StatusNotFound, post(push, true).Code)

			// a genuine signature speaking for another repository does not verify
			other, err := repokey.Parse("https://" + shape.Host + "/someone/else")
			require.NoError(t, err)
			foreign, err := f.DeliveryFor(hook, other, "refs/heads/main", commit)
			require.NoError(t, err)
			require.Equal(t, http.StatusNotFound, post(foreign, false).Code)

			// one repository's secret does not sign a push to its sibling
			crossed, err := f.DeliveryFor(adminHook, key, "refs/heads/main", commit)
			require.NoError(t, err)
			require.Equal(t, http.StatusNotFound, post(crossed, false).Code)

			// the sibling's own push verifies under its own secret
			sibling, err := f.Delivery(adminHook, "refs/heads/main", commit)
			require.NoError(t, err)
			require.Equal(t, http.StatusAccepted, post(sibling, false).Code)
			queue.queued = queue.queued[1:]

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

func TestAHookThisCloudCannotCheckIsNotAnUnverifiedDelivery(t *testing.T) {
	reg, err := fake.New(fake.GitHubLike).Registry(forge.Policy{})
	require.NoError(t, err)
	forges, err := New(reg)
	require.NoError(t, err)

	_, err = forges.Verify(ingress.Hook{Repositories: []ingress.HookRepository{{Key: "codeberg.org/acme/pay", Secret: bytes.Repeat([]byte("k"), 32)}}}, http.Header{}, []byte("{}"))
	require.Error(t, err)
	require.NotErrorIs(t, err, ingress.ErrUnverified)

	_, err = forges.Verify(ingress.Hook{Repositories: []ingress.HookRepository{{Key: "not a key", Secret: []byte("k")}}}, http.Header{}, nil)
	require.ErrorContains(t, err, "names no repository")

	_, err = forges.Verify(ingress.Hook{Repositories: []ingress.HookRepository{{Key: "github.com/acme/pay"}}}, http.Header{}, nil)
	require.ErrorIs(t, err, ingress.ErrUnverified, "a repository with no secret verifies nothing")

	_, err = forges.Verify(ingress.Hook{Project: "p"}, http.Header{}, nil)
	require.ErrorContains(t, err, "no repositories")
}

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
	header.Set("X-GitHub-Event", "push")

	_, err = forges.Verify(ingress.Hook{
		Repositories: []ingress.HookRepository{{Key: f.Keys().Writable.String(), Secret: secret.Expose()}},
	}, header, body)
	require.ErrorIs(t, err, ingress.ErrMalformed)
}
