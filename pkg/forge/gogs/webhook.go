package gogs

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// Gogs sends X-Gogs-Signature, a bare hex HMAC-SHA256 of the body;
// verification is the core's (forge.SchemeGogsSHA256).
func (d *driver) Scheme() forge.Scheme { return forge.SchemeGogsSHA256 }

// pushPayload reads only what a Push needs: every field kept is one more thing
// downstream comes to trust.
type pushPayload struct {
	Ref        string       `json:"ref"`
	After      string       `json:"after"`
	Repository pushRepoJSON `json:"repository"`
}

type pushRepoJSON struct {
	CloneURL string `json:"clone_url"`
}

// Push reads a delivery the core has verified and returns what it claims; the
// repository is named by its clone URL. Only "after" is read: Gogs sends no
// head_commit, and reports a branch deletion as a separate "delete" event.
func (d *driver) Push(body forge.VerifiedBody, header http.Header) (forge.PushClaim, error) {
	if !strings.EqualFold(header.Get("X-Gogs-Event"), "push") {
		return forge.PushClaim{}, forge.ErrNotAPush
	}

	var p pushPayload
	if err := json.Unmarshal(body.Bytes(), &p); err != nil {
		return forge.PushClaim{}, fmt.Errorf("forge/gogs: unreadable push payload: %w", err)
	}
	switch {
	case p.Repository.CloneURL == "":
		return forge.PushClaim{}, errors.New("forge/gogs: push payload names no repository")
	case len(p.Repository.CloneURL) > maxHostString:
		return forge.PushClaim{}, errors.New("forge/gogs: push payload names a repository by an implausibly long clone URL")
	}
	// THE REASON IS NOT CARRIED: repokey quotes what it was given, and a clone
	// URL with an identity half would put whatever the payload's author chose
	// into a tau log.
	key, err := repokey.Parse(p.Repository.CloneURL)
	if err != nil {
		return forge.PushClaim{}, errors.New("forge/gogs: push payload names no repository this layer can address")
	}
	return forge.PushClaim{Repository: key, Ref: p.Ref, Commit: p.After}, nil
}
