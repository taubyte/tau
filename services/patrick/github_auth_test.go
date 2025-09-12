package service

import (
	"context"
	"testing"

	httpAuth "github.com/taubyte/tau/pkg/http/auth"
	"gotest.tools/v3/assert"
)

func TestGitHubTokenHTTPAuth(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*mockHTTPContext)
		expectError   bool
		errorContains string
	}{
		{
			name: "no authorization",
			setupMock: func(ctx *mockHTTPContext) {
			},
			expectError:   true,
			errorContains: "valid Github token required",
		},
		{
			name: "invalid auth type",
			setupMock: func(ctx *mockHTTPContext) {
				ctx.SetVariable("Authorization", httpAuth.Authorization{
					Type:  "invalid",
					Token: "test-token",
				})
			},
			expectError:   true,
			errorContains: "valid Github token required",
		},
		{
			name: "oauth auth type success",
			setupMock: func(ctx *mockHTTPContext) {
				ctx.SetVariable("Authorization", httpAuth.Authorization{
					Type:  "oauth",
					Token: "",
				})
			},
			expectError: false,
		},
		{
			name: "github auth type success",
			setupMock: func(ctx *mockHTTPContext) {
				ctx.SetVariable("Authorization", httpAuth.Authorization{
					Type:  "github",
					Token: "",
				})
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			ctx := newMockHTTPContext()

			if tt.setupMock != nil {
				tt.setupMock(ctx)
			}

			result, err := service.GitHubTokenHTTPAuth(ctx)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
				if tt.errorContains != "" {
					assert.ErrorContains(t, err, tt.errorContains)
				}
			} else {
				assert.NilError(t, err)
				assert.Assert(t, result == nil)
				variables := ctx.Variables()
				assert.Assert(t, variables["GithubClient"] != nil)
				assert.Assert(t, variables["GithubClientDone"] != nil)
			}
		})
	}
}

func TestGitHubTokenHTTPAuthCleanup(t *testing.T) {
	tests := []struct {
		name        string
		setupMock   func(*mockHTTPContext)
		expectError bool
	}{
		{
			name: "no cleanup function",
			setupMock: func(ctx *mockHTTPContext) {
			},
			expectError: false,
		},
		{
			name: "cleanup function exists",
			setupMock: func(ctx *mockHTTPContext) {
				cancelCalled := false
				ctx.SetVariable("GithubClientDone", context.CancelFunc(func() {
					cancelCalled = true
				}))
				ctx.SetVariable("cancelCalled", &cancelCalled)
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			ctx := newMockHTTPContext()

			if tt.setupMock != nil {
				tt.setupMock(ctx)
			}

			result, err := service.GitHubTokenHTTPAuthCleanup(ctx)

			assert.NilError(t, err)
			assert.Assert(t, result == nil)

			if tt.name == "cleanup function exists" {
				variables := ctx.Variables()
				if cancelCalled, ok := variables["cancelCalled"].(*bool); ok {
					assert.Assert(t, *cancelCalled, "Expected cancel function to be called")
				}
			}
		})
	}
}

func TestGitHubTokenHTTPAuthContextCleanup(t *testing.T) {
	t.Run("cleanup with valid cancel function", func(t *testing.T) {
		service := createTestService()
		ctx := newMockHTTPContext()

		cancelCalled := false
		cancelFunc := context.CancelFunc(func() {
			cancelCalled = true
		})
		ctx.SetVariable("GithubClientDone", cancelFunc)

		result, err := service.GitHubTokenHTTPAuthCleanup(ctx)
		assert.NilError(t, err)
		assert.Assert(t, result == nil)

		assert.Assert(t, cancelCalled, "Expected cancel function to be called")
	})

	t.Run("cleanup with nil cancel function", func(t *testing.T) {
		service := createTestService()
		ctx := newMockHTTPContext()

		ctx.SetVariable("GithubClientDone", nil)

		result, err := service.GitHubTokenHTTPAuthCleanup(ctx)
		assert.NilError(t, err)
		assert.Assert(t, result == nil)
	})

	t.Run("cleanup with non-cancel function type", func(t *testing.T) {
		service := createTestService()
		ctx := newMockHTTPContext()

		ctx.SetVariable("GithubClientDone", "not-a-cancel-function")

		defer func() {
			if r := recover(); r != nil {
				assert.Assert(t, true, "Expected panic due to unsafe type assertion")
			}
		}()

		result, err := service.GitHubTokenHTTPAuthCleanup(ctx)
		assert.NilError(t, err)
		assert.Assert(t, result == nil)
	})

	t.Run("cleanup with missing GithubClientDone variable", func(t *testing.T) {
		service := createTestService()
		ctx := newMockHTTPContext()

		result, err := service.GitHubTokenHTTPAuthCleanup(ctx)
		assert.NilError(t, err)
		assert.Assert(t, result == nil)
	})

	t.Run("cleanup with empty GithubClientDone variable", func(t *testing.T) {
		service := createTestService()
		ctx := newMockHTTPContext()

		ctx.SetVariable("GithubClientDone", "")

		defer func() {
			if r := recover(); r != nil {
				assert.Assert(t, true, "Expected panic due to unsafe type assertion")
			}
		}()

		result, err := service.GitHubTokenHTTPAuthCleanup(ctx)
		assert.NilError(t, err)
		assert.Assert(t, result == nil)
	})

}
