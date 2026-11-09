package cm

import (
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/core/service"
)

// sectioned is a host whose config is the section a node hands a service, decoded
// by yaml as a node decodes it.
func sectioned(t *testing.T, section map[string]any, running ...service.Running) hosting {
	t.Helper()
	i := newInstance(t, t.TempDir())
	i.config = func(into any) error {
		raw, err := yaml.Marshal(section)
		if err != nil {
			return err
		}
		return yaml.Unmarshal(raw, into)
	}
	return hosting{instance: i, running: running}
}

func issuingCM(extra map[string]any) map[string]any {
	acme := map[string]any{"enabled": true}
	for k, v := range extra {
		acme[k] = v
	}
	return map[string]any{"certificate": map[string]any{"acme": acme}}
}

func TestACloudIssuingNothingGivesAServiceNoCertificates(t *testing.T) {
	h := sectioned(t, map[string]any{"cloudfqdn": "tau.example"})
	c, err := ClientOn(t.Context(), h)
	require.NoError(t, err)
	require.Nil(t, c)
	challenges, err := ChallengesOn(t.Context(), h)
	require.NoError(t, err)
	require.Nil(t, challenges)
}

func TestAServiceTakingCertificatesNeedsTheZone(t *testing.T) {
	_, err := ClientOn(t.Context(), sectioned(t, map[string]any{"cm": issuingCM(nil)}))
	require.ErrorIs(t, err, ErrNoZone)
}

func TestAServiceBuildsItsOwnClient(t *testing.T) {
	for name, running := range map[string][]service.Running{
		"over the node's databases": {{Service: databases{}}},
		"over a store of its own":   nil,
	} {
		t.Run(name, func(t *testing.T) {
			h := sectioned(t, map[string]any{"cloudfqdn": "tau.example", "cm": issuingCM(nil)}, running...)
			c, err := ClientOn(t.Context(), h)
			require.NoError(t, err)
			require.NotNil(t, c.TLSConfig().GetCertificate)
			require.NoError(t, c.Close())
			require.NoError(t, c.Close(), "closing twice closes nothing twice")
		})
	}
}

func TestAClientWhoseStoreOrConfigFailsIsRefused(t *testing.T) {
	broken := sectioned(t, map[string]any{"cloudfqdn": "tau.example", "cm": issuingCM(nil)},
		service.Running{Service: databases{err: errors.New("disk gone")}})
	_, err := ClientOn(t.Context(), broken)
	require.ErrorContains(t, err, "disk gone")

	half := sectioned(t, map[string]any{"cloudfqdn": "tau.example", "cm": issuingCM(map[string]any{"eab": map[string]any{"kid": "half"}})})
	_, err = ClientOn(t.Context(), half)
	require.ErrorContains(t, err, "external account binding")
}

func TestTheResolverAProofIsLookedUpThroughIsConfig(t *testing.T) {
	r, err := Config{}.resolver()
	require.NoError(t, err)
	require.Same(t, net.DefaultResolver, r, "empty is the machine's own")

	r, err = Config{Resolver: "127.0.0.1:5353"}.resolver()
	require.NoError(t, err)
	require.IsType(t, &net.Resolver{}, r)

	_, err = Config{Resolver: "no port"}.resolver()
	require.ErrorContains(t, err, "not a host:port")

	refused := hosting{instance: zoned(t, Config{Resolver: "no port"})}
	require.ErrorContains(t, New().Start(t.Context(), refused), "not a host:port")
}
