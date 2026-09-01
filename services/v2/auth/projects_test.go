package auth

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/auth/v1"
)

// ProjectService.Create and .Get carry no caller identity requirement (see
// authz.go), so they are covered as direct calls here. .List needs a real
// connect.ServerStream and is covered in rpc_test.go.

func TestProjectGet_NotFound(t *testing.T) {
	s := newTestService(t)
	h := &projectHandler{s}

	_, err := h.Get(t.Context(), connect.NewRequest(&authv1.ProjectRef{Id: "does-not-exist"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestProjectCreate_And_Get(t *testing.T) {
	s := newTestService(t)
	h := &projectHandler{s}

	res, err := h.Create(t.Context(), connect.NewRequest(&authv1.CreateProjectRequest{
		Name:                "demo",
		ConfigRepositoryKey: "github.com/acme/config",
		CodeRepositoryKey:   "github.com/acme/code",
	}))
	require.NoError(t, err)
	assert.Equal(t, "demo", res.Msg.GetName())
	require.NotEmpty(t, res.Msg.GetId())
	// CreateProjectResponse deliberately does not echo the repository keys
	// back (auth.proto's own comment on CreateProjectResponse).

	got, err := h.Get(t.Context(), connect.NewRequest(&authv1.ProjectRef{Id: res.Msg.GetId()}))
	require.NoError(t, err)
	assert.Equal(t, "demo", got.Msg.GetName())
	assert.Equal(t, "github.com/acme/config", got.Msg.GetConfigRepositoryKey())
	assert.Equal(t, "github.com/acme/code", got.Msg.GetCodeRepositoryKey())

	// The two repositories must now be linked to the new project, the way
	// v1's createProjectStream links them (store.go's linkRepositoryToProject).
	configKey, err := parseRepoKey("github.com/acme/config")
	require.NoError(t, err)
	link, err := s.kv.Get(t.Context(), repoProjectRow(configKey))
	require.NoError(t, err)
	assert.Equal(t, res.Msg.GetId(), string(link))
}

func TestProjectCreate_EmptyName(t *testing.T) {
	s := newTestService(t)
	h := &projectHandler{s}

	_, err := h.Create(t.Context(), connect.NewRequest(&authv1.CreateProjectRequest{
		Name:                "",
		ConfigRepositoryKey: "github.com/acme/config",
		CodeRepositoryKey:   "github.com/acme/code",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestProjectCreate_BadConfigKey(t *testing.T) {
	s := newTestService(t)
	h := &projectHandler{s}

	_, err := h.Create(t.Context(), connect.NewRequest(&authv1.CreateProjectRequest{
		Name:                "demo",
		ConfigRepositoryKey: "",
		CodeRepositoryKey:   "github.com/acme/code",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestProjectCreate_BadCodeKey(t *testing.T) {
	s := newTestService(t)
	h := &projectHandler{s}

	_, err := h.Create(t.Context(), connect.NewRequest(&authv1.CreateProjectRequest{
		Name:                "demo",
		ConfigRepositoryKey: "github.com/acme/config",
		CodeRepositoryKey:   "",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// TestProjectCreate_RepositoriesNeedNotBeRegistered mirrors v1's own
// createProjectStream, which links whatever repository keys it is given
// without requiring either to have gone through Register first
// (projects.go's Create doc explains why this package does not add that
// requirement).
func TestProjectCreate_RepositoriesNeedNotBeRegistered(t *testing.T) {
	s := newTestService(t)
	h := &projectHandler{s}

	_, err := h.Create(t.Context(), connect.NewRequest(&authv1.CreateProjectRequest{
		Name:                "demo",
		ConfigRepositoryKey: "github.com/acme/config",
		CodeRepositoryKey:   "github.com/acme/code",
	}))
	assert.NoError(t, err)
}

// The three tests below reach Create's own three internal-error branches —
// putProject failing, and each of the two linkRepositoryToProject calls
// failing — each with a real store failure injected at exactly that seam
// (store_failures_test.go), not a narrowed check.

func TestProjectCreate_PutProjectFails(t *testing.T) {
	s := &Service{kv: &batchFailingKVDB{Store: newStore(t), failAt: 1}}
	h := &projectHandler{s}

	_, err := h.Create(t.Context(), connect.NewRequest(&authv1.CreateProjectRequest{
		Name: "demo", ConfigRepositoryKey: "github.com/acme/config", CodeRepositoryKey: "github.com/acme/code",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestProjectCreate_LinkingConfigFails(t *testing.T) {
	s := &Service{kv: &putFailsAfter{Store: newStore(t), failOn: 1}}
	h := &projectHandler{s}

	_, err := h.Create(t.Context(), connect.NewRequest(&authv1.CreateProjectRequest{
		Name: "demo", ConfigRepositoryKey: "github.com/acme/config", CodeRepositoryKey: "github.com/acme/code",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestProjectCreate_LinkingCodeFails(t *testing.T) {
	s := &Service{kv: &putFailsAfter{Store: newStore(t), failOn: 2}}
	h := &projectHandler{s}

	_, err := h.Create(t.Context(), connect.NewRequest(&authv1.CreateProjectRequest{
		Name: "demo", ConfigRepositoryKey: "github.com/acme/config", CodeRepositoryKey: "github.com/acme/code",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}
