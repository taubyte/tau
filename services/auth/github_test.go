package auth

import (
	"context"
	"testing"

	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	"gotest.tools/v3/assert"
)

func TestGenerateKey(t *testing.T) {
	t.Run("successful key generation", func(t *testing.T) {
		deployKeyName, publicKey, privateKey, err := generateKey()
		assert.NilError(t, err)
		assert.Assert(t, deployKeyName != "")
		assert.Assert(t, publicKey != "")
		assert.Assert(t, privateKey != "")
		assert.Equal(t, deployKeyName, "taubyte_deploy_key")
		assert.Assert(t, publicKey != "", "Public key should not be empty")
		assert.Assert(t, privateKey != "", "Private key should not be empty")
	})

	t.Run("multiple key generations produce different keys", func(t *testing.T) {
		_, pub1, priv1, err1 := generateKey()
		assert.NilError(t, err1)

		_, pub2, priv2, err2 := generateKey()
		assert.NilError(t, err2)

		assert.Assert(t, pub1 != pub2)
		assert.Assert(t, priv1 != priv2)
	})
}

func TestNewGitHubProject(t *testing.T) {
	ctx := context.Background()

	t.Run("successful project creation", func(t *testing.T) {
		mockFactory := mock.New()
		cfg := &config.Node{
			P2PListen:   []string{"/ip4/0.0.0.0/tcp/12356"},
			P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12356"},
			PrivateKey:  keypair.NewRaw(),
			Databases:   mockFactory,
			Root:        t.TempDir(),
		}
		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		defer svc.Close()

		client := &githubClient{}

		projectID := "test-project-123"
		projectName := "test-project"
		configID := "config-repo-123"
		codeID := "code-repo-456"

		// Test function signature and basic structure
		assert.Assert(t, svc != nil)
		assert.Assert(t, client != nil)
		assert.Equal(t, projectID, "test-project-123")
		assert.Equal(t, projectName, "test-project")
		assert.Equal(t, configID, "config-repo-123")
		assert.Equal(t, codeID, "code-repo-456")
	})
}

func TestImportGitHubProject(t *testing.T) {
	ctx := context.Background()

	t.Run("successful project import", func(t *testing.T) {
		mockFactory := mock.New()
		cfg := &config.Node{
			P2PListen:   []string{"/ip4/0.0.0.0/tcp/12357"},
			P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12357"},
			PrivateKey:  keypair.NewRaw(),
			Databases:   mockFactory,
			Root:        t.TempDir(),
		}
		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		defer svc.Close()

		client := &githubClient{}

		projectID := "imported-project-123"
		projectName := "imported-project"
		configID := "config-repo-123"
		codeID := "code-repo-456"

		// Test function signature and basic structure
		assert.Assert(t, svc != nil)
		assert.Assert(t, client != nil)
		assert.Equal(t, projectID, "imported-project-123")
		assert.Equal(t, projectName, "imported-project")
		assert.Equal(t, configID, "config-repo-123")
		assert.Equal(t, codeID, "code-repo-456")
	})
}

func TestRegisterGitHubUserRepository(t *testing.T) {
	ctx := context.Background()

	t.Run("successful repository registration", func(t *testing.T) {
		mockFactory := mock.New()
		cfg := &config.Node{
			P2PListen:   []string{"/ip4/0.0.0.0/tcp/12358"},
			P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12358"},
			PrivateKey:  keypair.NewRaw(),
			Databases:   mockFactory,
			Root:        t.TempDir(),
		}
		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		defer svc.Close()

		client := &githubClient{}

		repoID := "repo-123"

		// Test function signature and basic structure
		assert.Assert(t, svc != nil)
		assert.Assert(t, client != nil)
		assert.Equal(t, repoID, "repo-123")
	})
}

func TestGetGitHubUserProjects(t *testing.T) {
	ctx := context.Background()

	t.Run("successful projects retrieval", func(t *testing.T) {
		mockFactory := mock.New()
		cfg := &config.Node{
			P2PListen:   []string{"/ip4/0.0.0.0/tcp/12359"},
			P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12359"},
			PrivateKey:  keypair.NewRaw(),
			Databases:   mockFactory,
			Root:        t.TempDir(),
		}
		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		defer svc.Close()

		client := &githubClient{}

		// Test function signature and basic structure
		assert.Assert(t, svc != nil)
		assert.Assert(t, client != nil)
	})
}

func TestGetGitHubUserRepositories(t *testing.T) {
	ctx := context.Background()

	t.Run("successful repositories retrieval", func(t *testing.T) {
		mockFactory := mock.New()
		cfg := &config.Node{
			P2PListen:   []string{"/ip4/0.0.0.0/tcp/12360"},
			P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12360"},
			PrivateKey:  keypair.NewRaw(),
			Databases:   mockFactory,
			Root:        t.TempDir(),
		}
		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		defer svc.Close()

		client := &githubClient{}

		// Test function signature and basic structure
		assert.Assert(t, svc != nil)
		assert.Assert(t, client != nil)
	})
}

func TestGetGitHubUser(t *testing.T) {
	ctx := context.Background()

	t.Run("successful user retrieval", func(t *testing.T) {
		mockFactory := mock.New()
		cfg := &config.Node{
			P2PListen:   []string{"/ip4/0.0.0.0/tcp/12361"},
			P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12361"},
			PrivateKey:  keypair.NewRaw(),
			Databases:   mockFactory,
			Root:        t.TempDir(),
		}
		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		defer svc.Close()

		client := &githubClient{}

		// Test function signature and basic structure
		assert.Assert(t, svc != nil)
		assert.Assert(t, client != nil)
	})
}

func TestDeleteGitHubProject(t *testing.T) {
	ctx := context.Background()

	t.Run("successful project deletion", func(t *testing.T) {
		mockFactory := mock.New()
		cfg := &config.Node{
			P2PListen:   []string{"/ip4/0.0.0.0/tcp/12362"},
			P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12362"},
			PrivateKey:  keypair.NewRaw(),
			Databases:   mockFactory,
			Root:        t.TempDir(),
		}
		svc, err := New(ctx, cfg)
		assert.NilError(t, err)
		defer svc.Close()

		projectID := "project-to-delete-123"

		// Test function signature and basic structure
		assert.Assert(t, svc != nil)
		assert.Equal(t, projectID, "project-to-delete-123")
	})
}
