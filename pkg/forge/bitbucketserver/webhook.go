package bitbucketserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
)

// Scheme is what this driver registers NEW hooks with. Bitbucket Server signs
// a delivery from a hook with a secret as X-Hub-Signature: "sha256=" and the
// lowercase hex HMAC-SHA256 of the raw body — forge.SchemeBitbucketSHA256.
// There is no verification code here.
func (d *driver) Scheme() forge.Scheme { return forge.SchemeBitbucketSHA256 }

// pushPayload is the whole of what this driver reads out of a
// repo:refs_changed delivery.
type pushPayload struct {
	Repository struct {
		Slug    string `json:"slug"`
		Project struct {
			Key string `json:"key"`
		} `json:"project"`
	} `json:"repository"`
	Changes []struct {
		RefID  string `json:"refId"`
		ToHash string `json:"toHash"`
		Type   string `json:"type"`
	} `json:"changes"`
}

// Push reads a delivery the core HAS ALREADY VERIFIED and returns what it
// claims; the core compares the repository claimed against the hook's.
//
// The repository is named by project key and slug, which is this driver's key
// exactly. A deletion carries the all-zero toHash, which the core turns into
// forge.ErrRefDeleted. ONE DELIVERY, ONE REF: a push moving several refs is
// one delivery, and a claim has room for one, so more than one is refused
// rather than half-read.
func (d *driver) Push(body forge.VerifiedBody, header http.Header) (forge.PushClaim, error) {
	if header.Get("X-Event-Key") != pushEvent {
		return forge.PushClaim{}, forge.ErrNotAPush
	}
	var p pushPayload
	if err := json.Unmarshal(body.Bytes(), &p); err != nil {
		return forge.PushClaim{}, fmt.Errorf("forge/bitbucketserver: unreadable push payload: %w", err)
	}
	key, err := d.keyOf(p.Repository.Project.Key, p.Repository.Slug)
	if err != nil {
		return forge.PushClaim{}, fmt.Errorf("forge/bitbucketserver: push payload %w", err)
	}
	switch n := len(p.Changes); {
	case n == 0:
		return forge.PushClaim{}, errors.New("forge/bitbucketserver: push payload carries no ref change")
	case n > 1:
		return forge.PushClaim{}, fmt.Errorf("forge/bitbucketserver: push payload carries %d ref changes and a claim names one; refusing to pick one and drop the rest", n)
	}
	change := p.Changes[0]
	if !strings.HasPrefix(change.RefID, "refs/heads/") && !strings.HasPrefix(change.RefID, "refs/tags/") {
		return forge.PushClaim{}, fmt.Errorf("forge/bitbucketserver: push payload changes %q, which is not a branch or a tag", bounded(change.RefID))
	}
	return forge.PushClaim{Repository: key, Ref: change.RefID, Commit: change.ToHash}, nil
}
