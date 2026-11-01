package gitea

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// Gitea and Forgejo send X-Hub-Signature-256 exactly as GitHub does, so this is
// the github driver's scheme; verification is the core's.
func (d *driver) Scheme() forge.Scheme { return forge.SchemeHubSHA256 }

// pushPayload reads only what a Push needs: every field kept is one more thing
// downstream comes to trust.
type pushPayload struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Repository struct {
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
	HeadCommit *struct {
		ID string `json:"id"`
	} `json:"head_commit"`
}

// Push reads a delivery the core has verified and returns what it claims; the
// core compares the repository with the hook's. The repository is named by its
// clone URL, keyed by the same grammar as the registration.
func (d *driver) Push(body forge.VerifiedBody, header http.Header) (forge.PushClaim, error) {
	// Gitea names the event in a header, not in the payload. Forgejo sends
	// its own X-Forgejo-Event and still sends this one, so one header serves
	// both; a delivery that names a push in neither is not a push.
	if !strings.EqualFold(header.Get("X-Gitea-Event"), "push") {
		return forge.PushClaim{}, forge.ErrNotAPush
	}

	var p pushPayload
	if err := json.Unmarshal(body.Bytes(), &p); err != nil {
		return forge.PushClaim{}, fmt.Errorf("forge/gitea: unreadable push payload: %w", err)
	}
	switch {
	case p.Repository.CloneURL == "":
		return forge.PushClaim{}, errors.New("forge/gitea: push payload names no repository")
	case len(p.Repository.CloneURL) > maxHostString:
		return forge.PushClaim{}, errors.New("forge/gitea: push payload names a repository by an implausibly long clone URL")
	}
	// The reason is not carried: repokey quotes what it was given, and a clone URL
	// with an identity half would put a credential into a tau log.
	key, err := repokey.Parse(p.Repository.CloneURL)
	if err != nil {
		return forge.PushClaim{}, errors.New("forge/gitea: push payload names no repository this layer can address")
	}

	// "after" is the ref's new tip and is present on every push INCLUDING a
	// deletion, where it is the all-zero object id the core turns into
	// ErrRefDeleted. head_commit is the same commit when Gitea sends one and
	// null on a deletion, so it is the fallback and not the first read.
	commit := p.After
	if commit == "" && p.HeadCommit != nil {
		commit = p.HeadCommit.ID
	}
	return forge.PushClaim{Repository: key, Ref: p.Ref, Commit: commit}, nil
}
