package boot

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	secretsService "github.com/taubyte/tau/services/secrets"
)

func bundleOf(t *testing.T, ref secretsService.Ref, value string) *secretsv2.InitBundle {
	t.Helper()
	sealed, err := secretsService.Sealed(ref, []byte(value), time.Now())
	require.NoError(t, err)
	return &secretsv2.InitBundle{Secrets: []*secretsv2.InitSecret{sealed}}
}

func TestInitWritesTheVaultTheNodeThenReads(t *testing.T) {
	f := admit(t, bare(t))
	ref := secretsService.Ref{Scope: secretsService.Project("p", "e"), Name: "n"}
	bundle := bundleOf(t, ref, "v")

	require.ErrorIs(t, Init(t.Context(), f, bundle), instance.ErrNoSecretsHere)

	f.Node.Services = append(f.Node.Services, instance.Secrets)
	minted := f
	minted.PrivateKey = ""
	require.ErrorIs(t, Init(t.Context(), minted, bundle), ErrNoIdentity)
	broken := f
	broken.CA, broken.Certificate = "", ""
	require.ErrorIs(t, Init(t.Context(), broken, bundle), ErrNoCertificate)
	require.Error(t, Init(t.Context(), f, &secretsv2.InitBundle{Secrets: []*secretsv2.InitSecret{{}}}))

	require.NoError(t, Init(t.Context(), f, bundle))
	running, err := Start(t.Context(), f)
	require.NoError(t, err)
	t.Cleanup(func() { running.Close() })
	require.Eventually(t, func() bool {
		got, err := running.Vault().Secret(t.Context(), "p", "e", "n")
		return err == nil && got == "v"
	}, time.Minute, 50*time.Millisecond)
}
