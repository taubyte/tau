//go:build dreaming

package dream_test

import (
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
)

func TestAServiceIsResolvedByNameToTheNodeRunningIt(t *testing.T) {
	const cloud = "tau.dream.test"

	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Copies: 2,
		Configure: func(index int, c *instance.Config) {
			c.CloudFqdn = cloud
			if index > 0 {
				c.Services = []string{instance.Stats, instance.PLM}
			}
		},
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	ask := asking(t, nodeOf(t, u.Pids()[0]))
	name := instance.PLM + "." + cloud

	var reply *dns.Msg
	require.Eventually(t, func() bool {
		reply = ask(name, dns.TypeA)
		return reply.Rcode == dns.RcodeSuccess && len(reply.Answer) > 0
	}, 60*time.Second, 500*time.Millisecond, "the frontend's name never resolved")
	a, ok := reply.Answer[0].(*dns.A)
	require.True(t, ok, "answered a %T", reply.Answer[0])
	require.Equal(t, "127.0.0.1", a.A.String(), "a cloud binds loopback only")

	// The dns node's own service is a name too.
	require.Eventually(t, func() bool {
		r := ask(instance.DNS+"."+cloud, dns.TypeA)
		return r.Rcode == dns.RcodeSuccess && len(r.Answer) > 0
	}, 30*time.Second, 500*time.Millisecond)

	// Below a service nothing is published: absent, not unserved.
	require.Equal(t, dns.RcodeNameError, ask("x."+name, dns.TypeA).Rcode)
}
