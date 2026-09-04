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

// Scheme is what this driver registers NEW hooks with. It does not implement
// one: verification is the core's, once, for every forge that signs this way.
//
// It is named after the header family and not after GitHub because Gitea,
// Forgejo and Codeberg sign X-Hub-Signature-256 byte for byte identically — so
// a Gitea driver names this same constant and contains no verification code
// either, and there is exactly one HMAC loop between the four of them.
//
// GitHub also sends the SHA-1 sibling in X-Hub-Signature. The core deliberately
// does not implement it, and this driver deliberately does not ask for it:
// every delivery that carries it also carries the SHA-256 header, so reading it
// would only add a weaker signature to accept.
func (d *driver) Scheme() forge.Scheme { return forge.SchemeHubSHA256 }

// pushPayload is the whole of what this driver reads out of a push delivery.
//
// GitHub sends about ninety fields; the commit list, the pusher, the sender,
// the timestamps, the repository's numeric id and its owner object are all
// absent here on purpose. A delivery is a stranger's bytes that a forge
// relayed, and every field that survives into a Push is one more thing
// something downstream comes to trust.
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

// Push reads a delivery the core HAS ALREADY VERIFIED — the parameter type is
// the proof, since VerifiedBody's fields are unexported and no package outside
// forge can build a non-empty one — and returns what the payload CLAIMS. It
// decides nothing: the core compares the repository claimed here against the
// one the hook was registered for, and rejects a mismatch.
//
// THE REPOSITORY IS NAMED BY ITS CLONE URL AND BY NOTHING ELSE. GitHub also
// sends full_name and a numeric id; full_name is a name with no host, and the
// id is the thing v2's vocabulary does not have. The clone URL is what repokey
// derives a key from everywhere else in this tree, so the delivery and the
// registration are filed under the same grammar.
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
	// THE REASON IS NOT CARRIED, which is the same rule repository() keeps. A
	// delivery is a stranger bytes that a signature proves the forge RELAYED,
	// up to the core cap, and repokey quotes what it was given — so a clone URL
	// with an identity half, the one shape repokey calls a secret at rest,
	// would be written into a tau log by whoever composed the payload.
	// Measured: a delivery naming
	// https://x-access-token:<token>@github.com/acme/pay.git put that token in
	// the error.
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
