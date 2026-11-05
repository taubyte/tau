package auth

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-github/v71/github"
)

func TestVerifiedUser(t *testing.T) {
	var calls, fail atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"login":"u"}`))
	}))
	defer srv.Close()

	client := github.NewClient(nil)
	client.BaseURL, _ = url.Parse(srv.URL + "/")
	ctx := context.Background()

	oldTTL, oldMax := verifiedTokenTTL, verifiedTokenMax
	defer func() { verifiedTokenTTL, verifiedTokenMax = oldTTL, oldMax }()

	verifiedTokenTTL = time.Minute
	for range 3 {
		if _, err := verifiedUser(ctx, client, "cached"); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("%d calls to GitHub", calls.Load())
	}

	verifiedTokensLock.Lock()
	_, hashed := verifiedTokens[sha256.Sum256([]byte("cached"))]
	verifiedTokensLock.Unlock()
	if !hashed {
		t.Fatal("not keyed by the token's hash")
	}

	// an expired entry is checked again, and a token now refused stays refused
	verifiedTokenTTL = -time.Second
	if _, err := verifiedUser(ctx, client, "revoked"); err != nil {
		t.Fatal(err)
	}
	fail.Store(1)
	if _, err := verifiedUser(ctx, client, "revoked"); err == nil {
		t.Fatal("revoked token accepted after expiry")
	}
	fail.Store(0)

	// the cache stays bounded
	verifiedTokenTTL = time.Minute
	verifiedTokenMax = 2
	for _, tok := range []string{"a", "b", "c", "d"} {
		if _, err := verifiedUser(ctx, client, tok); err != nil {
			t.Fatal(err)
		}
	}
	verifiedTokensLock.Lock()
	defer verifiedTokensLock.Unlock()
	if len(verifiedTokens) > verifiedTokenMax {
		t.Fatalf("%d entries", len(verifiedTokens))
	}
}
