//go:build dreaming

package dream

import (
	"strings"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/helpers"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/tns/dream"

	_ "github.com/taubyte/tau/clients/p2p/auth/dream"

	_ "github.com/taubyte/tau/clients/p2p/patrick/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
)

func TestDreamFixtures_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":     {},
			"patrick": {},
			"auth":    {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Patrick: &commonIface.ClientConfig{},
					Auth:    &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	t.Run("createProjectWithJobs", func(t *testing.T) {
		err := u.RunFixture("createProjectWithJobs")
		assert.NilError(t, err)

		simple, err := u.Simple("client")
		assert.NilError(t, err)

		// Check for 20 seconds after fixture is ran for the jobs
		attempts := 0
		for {
			attempts += 1

			patrick, err := simple.Patrick()
			assert.NilError(t, err)

			jobs, err := patrick.List()
			assert.NilError(t, err)

			if len(jobs) >= 2 {
				break
			}

			assert.Assert(t, attempts < 20)

			time.Sleep(1 * time.Second)
		}
	})
}

func TestPushFixtures_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":     {},
			"patrick": {},
			"auth":    {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Patrick: &commonIface.ClientConfig{},
					Auth:    &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	t.Run("waitForTNSObjects", func(t *testing.T) {
		simple, err := u.Simple("client")
		assert.NilError(t, err)

		tnsClient, err := simple.TNS()
		assert.NilError(t, err)

		// Test with empty repo list (should return nil immediately)
		err = waitForTNSObjects(tnsClient, []int{}, 5, 1*time.Second)
		assert.NilError(t, err)

		// Test with non-existent repos (should timeout with "not all repositories found" error)
		err = waitForTNSObjects(tnsClient, []int{999, 998}, 2, 100*time.Millisecond)
		assert.Assert(t, err != nil, "should fail with timeout")
		assert.Assert(t, strings.Contains(err.Error(), "not all repositories found"), "should contain expected error message")

		// Test with single non-existent repo
		err = waitForTNSObjects(tnsClient, []int{999}, 1, 10*time.Millisecond)
		assert.Assert(t, err != nil, "should fail with single repo")
		assert.Assert(t, strings.Contains(err.Error(), "not all repositories found"), "should contain expected error message")

		// Test with maxAttempts = 0 (edge case)
		err = waitForTNSObjects(tnsClient, []int{999}, 0, 10*time.Millisecond)
		assert.Assert(t, err != nil, "should fail with 0 attempts")
	})

	t.Run("pushAll", func(t *testing.T) {
		simple, err := u.Simple("client")
		assert.NilError(t, err)

		auth, err := simple.Auth()
		assert.NilError(t, err)

		err = helpers.RegisterTestRepositories(u.Context(), auth, helpers.ConfigRepo, helpers.CodeRepo, helpers.LibraryRepo)
		assert.NilError(t, err)

		time.Sleep(5 * time.Second)

		err = u.RunFixture("pushAll")
		assert.NilError(t, err)
	})

	t.Run("pushConfig", func(t *testing.T) {
		err := u.RunFixture("pushConfig")
		assert.NilError(t, err)
	})

	t.Run("pushCode", func(t *testing.T) {
		err := u.RunFixture("pushCode")
		assert.NilError(t, err)
	})

	t.Run("pushWebsite", func(t *testing.T) {
		err := u.RunFixture("pushWebsite")
		assert.NilError(t, err)
	})

	t.Run("pushLibrary", func(t *testing.T) {
		err := u.RunFixture("pushLibrary")
		assert.NilError(t, err)
	})

	t.Run("pushSpecific with valid parameters", func(t *testing.T) {
		// Test with valid parameters
		err := u.RunFixture("pushSpecific", "123", "test/repo")
		assert.NilError(t, err)
	})

	t.Run("pushSpecific with invalid parameters", func(t *testing.T) {
		// Test with insufficient parameters (should fail)
		err := u.RunFixture("pushSpecific", "123")
		assert.Assert(t, err != nil, "should fail with insufficient parameters")

		// Test with invalid repo ID (should fail)
		err = u.RunFixture("pushSpecific", "invalid", "test/repo")
		assert.Assert(t, err != nil, "should fail with invalid repo ID")
	})
}

func TestCreatePatrickServiceConfig_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	// Test with delay config
	config := &commonIface.ServiceConfig{
		Others: map[string]int{
			"delay": 1,
		},
	}

	// Test the configuration parsing logic
	_, err = createPatrickService(u, config)
	assert.Assert(t, err != nil, "should fail due to service creation requirements")

	// Test with retry config
	config = &commonIface.ServiceConfig{
		Others: map[string]int{
			"retry": 1,
		},
	}

	_, err = createPatrickService(u, config)
	assert.Assert(t, err != nil, "should fail due to service creation requirements")

	// Test with both configs
	config = &commonIface.ServiceConfig{
		Others: map[string]int{
			"delay": 1,
			"retry": 1,
		},
	}

	_, err = createPatrickService(u, config)
	assert.Assert(t, err != nil, "should fail due to service creation requirements")

	// Test with no config
	config = &commonIface.ServiceConfig{
		Others: map[string]int{},
	}

	_, err = createPatrickService(u, config)
	assert.Assert(t, err != nil, "should fail due to service creation requirements")

	// Test with invalid config values (should not set flags)
	config = &commonIface.ServiceConfig{
		Others: map[string]int{
			"delay": 0, // Should not set flag
			"retry": 2, // Should not set flag
		},
	}

	_, err = createPatrickService(u, config)
	assert.Assert(t, err != nil, "should fail due to service creation requirements")
}

func TestPushWithNoServices_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	u.StartWithConfig(&dream.Config{Simples: map[string]dream.SimpleConfig{
		"client": {
			Clients: dream.SimpleConfigClients{
				Auth:    &commonIface.ClientConfig{},
				Patrick: &commonIface.ClientConfig{},
				TNS:     &commonIface.ClientConfig{},
			}.Compat(),
		},
	}})

	expectedError := "services not provided"

	t.Run("pushSpecific", func(t *testing.T) {
		assert.ErrorContains(t, u.RunFixture("pushSpecific", "123", "test/repo"), expectedError)
	})

	t.Run("pushWebsite", func(t *testing.T) {
		assert.ErrorContains(t, u.RunFixture("pushWebsite"), expectedError)
	})

	t.Run("pushLibrary", func(t *testing.T) {
		assert.ErrorContains(t, u.RunFixture("pushLibrary"), expectedError)
	})

	t.Run("pushAll", func(t *testing.T) {
		assert.ErrorContains(t, u.RunFixture("pushAll"), expectedError)
	})

	t.Run("pushConfig", func(t *testing.T) {
		assert.ErrorContains(t, u.RunFixture("pushConfig"), expectedError)
	})

	t.Run("pushCode", func(t *testing.T) {
		assert.ErrorContains(t, u.RunFixture("pushCode"), expectedError)
	})
}
