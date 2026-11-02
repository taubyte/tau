package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// The scheme is named for the header family: Gitea, Forgejo and Codeberg sign
// X-Hub-Signature-256 identically. Verification is the core's. The SHA-1
// X-Hub-Signature always arrives beside it, so it is not read.
func (d *driver) Scheme() forge.Scheme { return forge.SchemeHubSHA256 }

// pushPayload reads only what a Push needs: every field kept is one more thing
// downstream comes to trust.
type pushPayload struct {
	Ref        string          `json:"ref"`
	After      string          `json:"after"`
	Repository pushRepoJSON    `json:"repository"`
	HeadCommit *headCommitJSON `json:"head_commit"`
}

type headCommitJSON struct {
	ID string `json:"id"`
}

type pushRepoJSON struct {
	FullName string `json:"full_name"`
	CloneURL string `json:"clone_url"`
}

// Push reads a delivery the core has verified and returns what it claims; the
// core compares the repository with the hook's. The repository is named by its
// clone URL, keyed by the same grammar as the registration.
func (d *driver) Push(body forge.VerifiedBody, header http.Header) (forge.PushClaim, error) {
	// GitHub names the event in a header, not in the payload — a ping, an
	// installation event and a push all arrive at the same callback.
	if !strings.EqualFold(header.Get("X-GitHub-Event"), "push") {
		return forge.PushClaim{}, forge.ErrNotAPush
	}

	var p pushPayload
	if err := json.Unmarshal(body.Bytes(), &p); err != nil {
		return forge.PushClaim{}, fmt.Errorf("forge/github: unreadable push payload: %w", err)
	}
	switch {
	case p.Repository.CloneURL == "":
		return forge.PushClaim{}, errors.New("forge/github: push payload names no repository")
	case len(p.Repository.CloneURL) > maxHostString:
		return forge.PushClaim{}, errors.New("forge/github: push payload names a repository by an implausibly long clone URL")
	}
	// The reason is not carried: repokey quotes what it was given, and a clone URL
	// with an identity half would put a credential into a tau log.
	key, err := repokey.Parse(p.Repository.CloneURL)
	if err != nil {
		return forge.PushClaim{}, errors.New("forge/github: push payload names no repository this layer can address")
	}

	// "after" is the ref's new tip and is present on every push INCLUDING a
	// deletion, where it is the all-zero object id the core turns into
	// ErrRefDeleted. head_commit is the same commit when GitHub sends one and
	// null on a deletion, so it is the fallback and not the first read.
	commit := p.After
	if commit == "" && p.HeadCommit != nil {
		commit = p.HeadCommit.ID
	}
	return forge.PushClaim{Repository: key, Ref: p.Ref, Commit: commit}, nil
}
