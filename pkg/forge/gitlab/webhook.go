package gitlab

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// Scheme names the procedure this driver registers NEW hooks with. It does not
// implement one, and it is not the procedure a delivery is checked under: that
// is the scheme stored with the hook, so an instance upgraded from a shared
// token to a signing one keeps verifying its older hooks correctly and no
// migration is needed to avoid dropping pushes.
func (d *driver) Scheme() forge.Scheme { return d.cfg.scheme }

// pushPayload is the slice of GitLab's push event this driver reads, and reading
// this little is deliberate. A delivery is a stranger's bytes that a signature
// proves the instance relayed — not bytes that are true — so every field kept is
// one more thing something later trusts. The numeric project id, the commit
// list, the pusher, the timestamps and the whole deprecated top-level
// "repository" block are all present in the real payload and all ignored.
type pushPayload struct {
	ObjectKind string `json:"object_kind"`
	Ref        string `json:"ref"`
	After      string `json:"after"`

	// CheckoutSHA is null when the push deletes a ref, which is why After is the
	// fallback: on a deletion After is git's all-zero sentinel, and that is what
	// turns the delivery into forge.ErrRefDeleted instead of a build with no
	// commit.
	CheckoutSHA string `json:"checkout_sha"`

	Project struct {
		GitHTTPURL string `json:"git_http_url"`
	} `json:"project"`
}

// Push reads a delivery the core HAS ALREADY VERIFIED — the parameter type is
// the proof, since forge.VerifiedBody's fields are unexported and no package
// outside forge can build a non-empty one — and returns what the payload CLAIMS.
// It decides nothing: the core compares the claimed repository against the one
// the hook was registered for.
//
// THE EVENT IS NAMED IN A HEADER, IN PROSE. GitLab writes "Push Hook", with a
// space and a capital, where GitHub writes "push"; a tag push is a different
// event under a different name and is not a push here, because nothing in tau
// builds one. That the interface hands the raw http.Header to the driver rather
// than a normalised event string is what makes both spellings the driver's
// business and neither the core's.
func (d *driver) Push(body forge.VerifiedBody, header http.Header) (forge.PushClaim, error) {
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

	// The repository is named by ITS CLONE URL and derived through the one
	// grammar, so the payload and the config agree by construction rather than
	// by two readings of "owner/name".
	//
	// IT IS MEASURED BEFORE IT IS PARSED, and the reason it fails is not carried
	// — both for the reason repository gives. A delivery is a stranger's bytes
	// that a signature proves the instance relayed, up to the core's eight
	// megabyte cap, and repokey quotes what it was given; a key derived from an
	// implausible one would be stored and printed by everything downstream.
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
