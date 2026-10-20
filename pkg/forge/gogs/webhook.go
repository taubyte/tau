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

// Scheme is what this driver registers NEW hooks with. It does not implement
// one: Gogs sends X-Gogs-Signature, a bare hex HMAC-SHA256 over the body keyed
// by the hook's secret string, and that procedure lives in pkg/forge as
// SchemeGogsSHA256. The container suite proves it against a delivery a real
// Gogs signed.
func (d *driver) Scheme() forge.Scheme { return forge.SchemeGogsSHA256 }

// pushPayload is the whole of what this driver reads out of a push delivery.
//
// Gogs sends the commit list, the pusher, the sender, a compare URL and the
// repository's numeric id and owner; all of them are absent here on purpose. A
// delivery is a stranger's bytes that a forge relayed, and every field that
// survives into a Push is one more thing something downstream comes to trust.
type pushPayload struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Repository struct {
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
}

// Push reads a delivery the core HAS ALREADY VERIFIED — the parameter type is
// the proof — and returns what the payload CLAIMS. It decides nothing: the core
// compares the repository claimed here against the one the hook was registered
// for, and rejects a mismatch.
//
// THE REPOSITORY IS NAMED BY ITS CLONE URL AND BY NOTHING ELSE, for the gitea
// driver's reason: it is what repokey derives a key from everywhere else.
//
// "after" is the only commit read. Gogs sends no head_commit, and it reports a
// branch deletion as a separate "delete" event rather than as a push to the
// zero id; a push that does name the zero id is still handed to the core,
// which is what turns it into ErrRefDeleted.
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
