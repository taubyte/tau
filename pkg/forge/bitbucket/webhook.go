package bitbucket

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// Bitbucket Cloud signs a delivery as X-Hub-Signature: "sha256=" and the
// lowercase hex HMAC-SHA256 of the body. Verification is the core's.
func (d *driver) Scheme() forge.Scheme { return forge.SchemeBitbucketSHA256 }

// zeroCommit is what a deletion is reported as: the core turns it into
// forge.ErrRefDeleted.
var zeroCommit = strings.Repeat("0", 40)

type refJSON struct {
	Type   string     `json:"type"`
	Name   string     `json:"name"`
	Target targetJSON `json:"target"`
}

type targetJSON struct {
	Hash string `json:"hash"`
}

// pushPayload reads only what a Push needs: every field kept is one more thing
// downstream comes to trust.
type pushPayload struct {
	Repository pushRepoJSON `json:"repository"`
	Push       pushJSON     `json:"push"`
}

type pushJSON struct {
	Changes []changeJSON `json:"changes"`
}

type changeJSON struct {
	New *refJSON `json:"new"`
	Old *refJSON `json:"old"`
}

type pushRepoJSON struct {
	Links webhookLinksJSON `json:"links"`
}

type webhookLinksJSON struct {
	HTML hrefJSON `json:"html"`
}

type hrefJSON struct {
	Href string `json:"href"`
}

// Push reads a delivery the core has verified and returns what it claims; the
// core compares the repository with the hook's. The repository is named by its
// HTML link, which keys like a clone URL. A delivery moving several refs is
// refused: a claim has room for one, and picking one would drop the rest.
func (d *driver) Push(body forge.VerifiedBody, header http.Header) (forge.PushClaim, error) {
	if header.Get("X-Event-Key") != "repo:push" {
		return forge.PushClaim{}, forge.ErrNotAPush
	}

	var p pushPayload
	if err := json.Unmarshal(body.Bytes(), &p); err != nil {
		return forge.PushClaim{}, fmt.Errorf("forge/bitbucket: unreadable push payload: %w", err)
	}
	href := p.Repository.Links.HTML.Href
	switch {
	case href == "":
		return forge.PushClaim{}, errors.New("forge/bitbucket: push payload names no repository")
	case len(href) > maxHostString:
		return forge.PushClaim{}, errors.New("forge/bitbucket: push payload names a repository by an implausibly long link")
	}
	// The reason is not carried: repokey quotes what it was given, and a link
	// with a password in it would put that password in a tau log.
	key, err := repokey.Parse(href)
	if err != nil {
		return forge.PushClaim{}, errors.New("forge/bitbucket: push payload names no repository this layer can address")
	}

	switch n := len(p.Push.Changes); {
	case n == 0:
		return forge.PushClaim{}, errors.New("forge/bitbucket: push payload carries no ref change")
	case n > 1:
		return forge.PushClaim{}, fmt.Errorf("forge/bitbucket: push payload carries %d ref changes and a claim names one; refusing to pick one and drop the rest", n)
	}
	change := p.Push.Changes[0]

	side, commit := change.New, ""
	if side == nil {
		// A deletion: "new" is null and "old" names the ref that went.
		side, commit = change.Old, zeroCommit
	}
	if side == nil {
		return forge.PushClaim{}, errors.New("forge/bitbucket: push payload names a change with neither side")
	}
	if commit == "" {
		commit = side.Target.Hash
	}
	var ref string
	switch side.Type {
	case "branch", "named_branch":
		ref = "refs/heads/" + side.Name
	case "tag", "annotated_tag":
		ref = "refs/tags/" + side.Name
	default:
		return forge.PushClaim{}, fmt.Errorf("forge/bitbucket: push payload changes a ref of type %q, which is not a branch or a tag", bounded(side.Type))
	}
	if side.Name == "" {
		return forge.PushClaim{}, errors.New("forge/bitbucket: push payload changes a ref with no name")
	}
	return forge.PushClaim{Repository: key, Ref: ref, Commit: commit}, nil
}
