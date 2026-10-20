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

// Scheme is what this driver registers NEW hooks with. Bitbucket Cloud signs a
// delivery from a hook with a secret as X-Hub-Signature: "sha256=" and the
// lowercase hex HMAC-SHA256 of the raw body, keyed by the secret's bytes —
// forge.SchemeBitbucketSHA256. There is no verification code here.
func (d *driver) Scheme() forge.Scheme { return forge.SchemeBitbucketSHA256 }

// zeroCommit is what a deletion is reported as: the core turns it into
// forge.ErrRefDeleted.
var zeroCommit = strings.Repeat("0", 40)

// refJSON is one side of a ref change.
type refJSON struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Target struct {
		Hash string `json:"hash"`
	} `json:"target"`
}

// pushPayload is the whole of what this driver reads out of a repo:push
// delivery. Bitbucket sends the actor, the commit list, links and the
// repository's UUID besides; every field that survived into a Push would be one
// more thing something downstream comes to trust.
type pushPayload struct {
	Repository struct {
		Links struct {
			HTML struct {
				Href string `json:"href"`
			} `json:"html"`
		} `json:"links"`
	} `json:"repository"`
	Push struct {
		Changes []struct {
			New *refJSON `json:"new"`
			Old *refJSON `json:"old"`
		} `json:"changes"`
	} `json:"push"`
}

// Push reads a delivery the core HAS ALREADY VERIFIED and returns what it
// claims. It decides nothing: the core compares the repository claimed here
// against the one the hook was registered for.
//
// THE REPOSITORY IS NAMED BY ITS HTML LINK, because a Bitbucket push payload
// carries no clone URL; "https://bitbucket.org/acme/pay" is the same key
// repokey derives from the clone URL, which is what makes the comparison hold.
//
// ONE DELIVERY, ONE REF. Bitbucket batches several ref changes into a single
// delivery when one git push moves several refs. A claim has room for one ref,
// and picking one would drop the others without a word — so a delivery carrying
// more than one change is REFUSED, loudly, rather than half-read.
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
