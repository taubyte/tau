//go:build dreaming

package dream_test

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	cmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/cm/v2"
	pkgregister "github.com/taubyte/tau/pkg/register"
	buildDeploy "github.com/taubyte/tau/services/build/deploy"
	"github.com/taubyte/tau/services/build/run"
	"github.com/taubyte/tau/services/cm"
)

const (
	customDomainID = "QmQLHmWAjrRvtx1Ewo8XPUTbii48ht2jZvgBJ3Mi8B1C3o"
	stolenDomainID = "QmRsawqvCW6LJxceX5AoFr4KcrerdAxeJNvsbYE2x9rQYr"
)

// txtZone is the internet's DNS as far as these tests are concerned.
type txtZone struct {
	mu sync.Mutex
	m  map[string][]string
}

func (z *txtZone) set(name, value string) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.m[name] = []string{value}
}

func (z *txtZone) LookupTXT(_ context.Context, name string) ([]string, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if v, ok := z.m[name]; ok {
		return v, nil
	}
	return nil, errors.New("no such host")
}

type configures struct{}

func (configures) Configures(context.Context, string, string) (bool, error) { return true, nil }

type corpus struct{ dir string }

func (c corpus) Checkout(context.Context, string, string, string) (string, func(), error) {
	return c.dir, func() {}, nil
}

type buildLog struct{ strings.Builder }

func (*buildLog) Asset(context.Context, string, io.Reader) error { return nil }

// planThenRun is a push as the backend runs it: the plan, then one job per environment
// it names.
func planThenRun(ctx context.Context, d *buildDeploy.Deploy, w run.Work, out run.Output) error {
	environments, err := d.Plan(ctx, w, out)
	if err != nil {
		return err
	}
	for _, environment := range environments {
		w.Environment = environment
		if err := d.Run(ctx, w, out); err != nil {
			return err
		}
	}
	return nil
}

func TestACustomDomainDeploysOnlyOnceProven(t *testing.T) {
	dns := &txtZone{m: map[string][]string{}}
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Copies:   2,
		Services: []string{instance.Discovery, instance.Secrets, instance.CM},
		Options:  []instance.Option{instance.WithResolver(dns)},
		Configure: func(_ int, c *instance.Config) {
			c.CloudFqdn = "tau.dream.test"
		},
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	var members []libp2ppeer.ID
	for _, pid := range u.Pids() {
		id, err := libp2ppeer.Decode(pid)
		require.NoError(t, err)
		members = append(members, id)
	}
	domains, err := cm.NewDomains(u.Peers()[1].Host(), fleetOf(members))
	require.NoError(t, err)
	t.Cleanup(func() { domains.Close() })

	deployed := func(project, id string) (*pkgregister.Register, error) {
		r, err := pkgregister.New(pkgregister.WithStore(pkgregister.NewMemStore()))
		require.NoError(t, err)
		d, err := buildDeploy.New(
			buildDeploy.WithProjects(configures{}),
			buildDeploy.WithCheckout(corpus{filepath.Join("projects", project)}),
			buildDeploy.WithLocalRegister(r),
			buildDeploy.WithCloudFqdn("tau.dream.test"),
			buildDeploy.WithDomains(domains),
		)
		require.NoError(t, err)
		return r, planThenRun(t.Context(), d, run.Work{JobID: "j", RepositoryKey: "github.com/acme/" + id, Ref: "refs/heads/main", CommitSHA: "c", ProjectID: id}, &buildLog{})
	}

	_, err = deployed("with/domain/apex", customDomainID)
	require.ErrorIs(t, err, buildDeploy.ErrUnproven, "no record is set yet")

	// THE VAULT NEEDS ITS MEMBERS before the first signing key can be kept.
	var claim *cmv2.DomainClaim
	require.Eventually(t, func() bool {
		claim, err = domains.Claim(t.Context(), customDomainID, "shop.test")
		return err == nil
	}, time.Minute, time.Second, "a cm node signs a claim once the vault answers")
	require.Equal(t, customDomainID[:8]+".shop.test", claim.GetEntry())
	dns.set(claim.GetEntry(), claim.GetToken())

	var r *pkgregister.Register
	require.Eventually(t, func() bool {
		r, err = deployed("with/domain/apex", customDomainID)
		return err == nil
	}, time.Minute, time.Second, "the signing key replicates to every cm node")
	project, environment, err := r.ResolveHost(t.Context(), "www.shop.test")
	require.NoError(t, err)
	require.Equal(t, customDomainID, project)
	require.Equal(t, "prod", environment)

	dns.set(stolenDomainID[:8]+".shop.test", claim.GetToken())
	_, err = deployed("refused/domain/stolen", stolenDomainID)
	require.ErrorIs(t, err, buildDeploy.ErrUnproven)
	require.ErrorContains(t, err, "tau domain claim shop.test")

	// A ZONE UNDER A PROVEN ONE is its project's with no record of its own,
	// and another project's never, whatever record it sets.
	r, err = deployed("with/domain/child", customDomainID)
	require.NoError(t, err)
	project, _, err = r.ResolveHost(t.Context(), "www.eu.shop.test")
	require.NoError(t, err)
	require.Equal(t, customDomainID, project)

	stolen, err := domains.Claim(t.Context(), stolenDomainID, "eu.shop.test")
	require.NoError(t, err)
	dns.set(stolen.GetEntry(), stolen.GetToken())
	_, err = deployed("refused/domain/stolenchild", stolenDomainID)
	require.ErrorIs(t, err, buildDeploy.ErrUnproven)
	require.ErrorContains(t, err, "shop.test is proven to be another project's")

	// THE PARENT'S RECORD GONE, the child's redeploy is refused: a released
	// domain is lost at its next deploy.
	dns.set(claim.GetEntry(), "removed")
	_, err = deployed("with/domain/child", customDomainID)
	require.ErrorIs(t, err, buildDeploy.ErrUnproven)
}

// fleetOf is the cm nodes named, none of them beside the caller.
type fleetOf []libp2ppeer.ID

func (f fleetOf) Members() []libp2ppeer.ID { return slices.Clone(f) }
func (fleetOf) Local() *cm.Store           { return nil }
