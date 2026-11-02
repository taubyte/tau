package gitlab

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// Standard Webhooks is the only procedure GitLab offers that binds the body.
// Verification is the core's, under the scheme stored with the hook.
func (d *signingDriver) Scheme() forge.Scheme { return forge.SchemeStandardWebhooks }

func (d *signingMemberDriver) Scheme() forge.Scheme { return forge.SchemeStandardWebhooks }

func (d *signingDriver) Push(body forge.VerifiedBody, header http.Header) (forge.PushClaim, error) {
	return push(body, header)
}

func (d *signingMemberDriver) Push(body forge.VerifiedBody, header http.Header) (forge.PushClaim, error) {
	return push(body, header)
}

// pushPayload reads only what a Push needs: every field kept is one more thing
// downstream comes to trust.
type pushPayload struct {
	ObjectKind string `json:"object_kind"`
	Ref        string `json:"ref"`
	After      string `json:"after"`

	// CheckoutSHA is null when the push deletes a ref, which is why After is the
	// fallback: on a deletion After is git's all-zero sentinel, and that is what
	// turns the delivery into forge.ErrRefDeleted instead of a build with no
	// commit.
	CheckoutSHA string `json:"checkout_sha"`

	Project pushProjectJSON `json:"project"`
}

type pushProjectJSON struct {
	GitHTTPURL string `json:"git_http_url"`
}

// push reads a delivery the core has verified and returns what it claims. The
// event is named in a header, in prose: "Push Hook"; a tag push is not a push
// here, since nothing in tau builds one.
func push(body forge.VerifiedBody, header http.Header) (forge.PushClaim, error) {
	if header.Get("X-Gitlab-Event") != "Push Hook" {
		return forge.PushClaim{}, forge.ErrNotAPush
	}

	var payload pushPayload
	if err := json.Unmarshal(body.Bytes(), &payload); err != nil {
		return forge.PushClaim{}, fmt.Errorf("gitlab: delivery is not a readable push payload: %w", err)
	}
	if payload.ObjectKind != "push" {
		// The header and the body disagreeing is not a push either, and saying
		// so beats reading a body whose shape was never established.
		return forge.PushClaim{}, forge.ErrNotAPush
	}

	// The repository is named by its clone URL, through the one grammar. It is
	// measured before it is parsed, and the parse error is not carried, for
	// repository's reasons.
	switch cloneURL := payload.Project.GitHTTPURL; {
	case cloneURL == "":
		return forge.PushClaim{}, errors.New("gitlab: delivery names no repository")
	case len(cloneURL) > maxHostString:
		return forge.PushClaim{}, errors.New("gitlab: delivery names no repository, only a string too long to be a clone URL")
	}
	key, err := repokey.Parse(payload.Project.GitHTTPURL)
	if err != nil {
		return forge.PushClaim{}, errors.New("gitlab: delivery names no repository this layer can address")
	}

	commit := payload.CheckoutSHA
	if commit == "" {
		commit = payload.After
	}
	return forge.PushClaim{Repository: key, Ref: payload.Ref, Commit: commit}, nil
}
