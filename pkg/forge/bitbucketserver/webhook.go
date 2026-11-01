package bitbucketserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
)

// Bitbucket Server signs a delivery as X-Hub-Signature: "sha256=" and the
// lowercase hex HMAC-SHA256 of the body. Verification is the core's.
func (d *driver) Scheme() forge.Scheme { return forge.SchemeBitbucketSHA256 }

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

// Push reads a delivery the core has verified. A deletion carries the all-zero
// toHash. A delivery moving several refs is refused: a claim has room for one.
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
