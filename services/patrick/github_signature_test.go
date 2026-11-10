package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/dream/helpers"
	servicesCommon "github.com/taubyte/tau/services/common"
	"gotest.tools/v3/assert"
)

func hmac256(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func pushBody(t *testing.T, pushedAt time.Time) []byte {
	t.Helper()
	body, err := helpers.MakeTemplate(12345, "test/repo", "main", "", "", pushedAt.Unix())
	assert.NilError(t, err)
	return body
}

// postHook sends body with headers to a non-dev Patrick whose hook secret is "s3cret".
func postHook(t *testing.T, ts *testSetup, body []byte, headers map[string]string) (interface{}, error) {
	t.Helper()
	ts.ctx.headers = map[string]string{"X-GitHub-Event": "push", "Content-Type": "application/json"}
	ts.ctx.SetHeaders(headers)
	ts.ctx.SetBody(body)
	ts.setupSecretInContext("s3cret")
	return ts.service.githubHookHandler(ts.ctx)
}

func nonDev(t *testing.T) *testSetup {
	old := servicesCommon.FakeSecret
	servicesCommon.FakeSecret = false
	t.Cleanup(func() { servicesCommon.FakeSecret = old })
	return createTestSetup(false)
}

func pushes(ts *testSetup) int { return len(ts.service.jobQueue.(*mockJobQueue).pushCalls) }

func TestGithubHookSignature(t *testing.T) {
	body := pushBody(t, time.Now())
	good := "sha256=" + hmac256(body, "s3cret")

	tests := []struct {
		name    string
		headers map[string]string
		wantErr string
	}{
		{"unsigned", nil, "invalid X-Hub-Signature-256"},
		{"wrong sha256", map[string]string{"X-Hub-Signature-256": "sha256=" + hmac256(body, "other")}, "invalid X-Hub-Signature-256"},
		{"sha256 not hex", map[string]string{"X-Hub-Signature-256": "sha256=zz"}, "invalid X-Hub-Signature-256"},
		{"sha1 only", map[string]string{"X-Hub-Signature": "sha1=" + generateHMAC(body, "s3cret")}, "invalid X-Hub-Signature-256"},
		{"short sha1 with valid sha256", map[string]string{"X-Hub-Signature": "abc", "X-Hub-Signature-256": good}, ""},
		{"both valid", map[string]string{"X-Hub-Signature": "sha1=" + generateHMAC(body, "s3cret"), "X-Hub-Signature-256": good}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := nonDev(t)
			res, err := postHook(t, ts, body, tt.headers)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				assert.Equal(t, pushes(ts), 0)
				return
			}
			assert.NilError(t, err)
			assertJobResult(t, res)
			assert.Equal(t, pushes(ts), 1)
		})
	}
}

func TestGithubHookEmptySecretRefused(t *testing.T) {
	ts := nonDev(t)
	body := pushBody(t, time.Now())
	ts.ctx.SetBody(body)
	ts.ctx.SetHeaders(map[string]string{"X-GitHub-Event": "push"})
	ts.setupSecretInContext("")
	_, err := ts.service.githubHookHandler(ts.ctx)
	assert.ErrorContains(t, err, "hook has no secret")
	assert.Equal(t, pushes(ts), 0)
}

func TestGithubHookReplayOfArchivedJob(t *testing.T) {
	ts := nonDev(t)
	body := pushBody(t, time.Now())
	sig := map[string]string{"X-Hub-Signature-256": "sha256=" + hmac256(body, "s3cret")}

	r1, err := postHook(t, ts, body, sig)
	assert.NilError(t, err)
	j1 := r1.(*patrick.Job)

	// the build finished: the job moved to the archive
	ctx := context.Background()
	data, err := ts.mockDB.Get(ctx, "/jobs/"+j1.Id)
	assert.NilError(t, err)
	assert.NilError(t, ts.mockDB.Put(ctx, "/archive/jobs/"+j1.Id, data))
	assert.NilError(t, ts.mockDB.Delete(ctx, "/jobs/"+j1.Id))

	r2, err := postHook(t, ts, body, sig)
	assert.NilError(t, err)
	assert.Equal(t, r2.(*patrick.Job).Id, j1.Id)
	assert.Equal(t, pushes(ts), 1)
	_, err = ts.mockDB.Get(ctx, "/jobs/"+j1.Id)
	assert.Assert(t, err != nil, "replay must not re-register the job")
}

func TestGithubHookOldPushRefused(t *testing.T) {
	ts := nonDev(t)
	body := pushBody(t, time.Now().Add(-48*time.Hour))
	_, err := postHook(t, ts, body, map[string]string{"X-Hub-Signature-256": "sha256=" + hmac256(body, "s3cret")})
	assert.ErrorContains(t, err, "older than")
	assert.Equal(t, pushes(ts), 0)
}

// Dream's inject signs SHA-1 with the fake secret and sends an empty SHA-256 (dream/helpers.PushJob).
func TestGithubHookDevModeAcceptsDreamInject(t *testing.T) {
	old := servicesCommon.FakeSecret
	servicesCommon.FakeSecret = true
	t.Cleanup(func() { servicesCommon.FakeSecret = old })

	ts := createTestSetup(true)
	body := pushBody(t, time.Now().Add(-48*time.Hour)) // age is not checked in dev mode
	ts.ctx.SetHeaders(map[string]string{
		"X-GitHub-Event":      "push",
		"X-Hub-Signature":     "sha1=" + generateHMAC(body, "taubyte_secret"),
		"X-Hub-Signature-256": "sha256=",
	})
	ts.ctx.SetBody(body)
	_, err := ts.service.githubCheckHookAndExtractSecret(ts.ctx)
	assert.NilError(t, err)

	res, err := ts.service.githubHookHandler(ts.ctx)
	assert.NilError(t, err)
	assertJobResult(t, res)
	assert.Equal(t, pushes(ts), 1)
}
