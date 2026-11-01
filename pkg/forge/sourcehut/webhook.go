package sourcehut

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
)

// The instance's Ed25519 signature and the hook's binding are checked by the
// core before Push is handed a body.
func (d *signingDriver) Scheme() forge.Scheme { return forge.SchemePayloadEd25519 }

// wireAlias is the shape forge.SchemePayloadEd25519's wire string has. It is
// checked before the string is written into a GraphQL document, because a
// document is code the forge runs.
var wireAlias = regexp.MustCompile(`^tau_[0-9a-f]{64}$`)

// hookQuery is the document a hook's deliveries answer. Its webhook half reads
// the push; its last line aliases __typename under the hook's wire string, so
// the forge echoes the binding into the body it signs.
func hookQuery(wire string) (string, error) {
	if !wireAlias.MatchString(wire) {
		return "", errors.New("forge/sourcehut: the hook's wire string is not the GraphQL alias forge.SchemePayloadEd25519 derives")
	}
	return `query {
  webhook {
    event
    ... on GitEvent {
      repository { name owner { canonicalName } }
      updates { ref { name } old { id } new { id } }
    }
  }
  ` + wire + `: __typename
}`, nil
}

// pushPayload is the whole of what this driver reads out of a delivery: the
// fields hookQuery asked for and nothing else.
type pushPayload struct {
	Data struct {
		Webhook struct {
			Event      string `json:"event"`
			Repository *struct {
				Name  string `json:"name"`
				Owner struct {
					CanonicalName string `json:"canonicalName"`
				} `json:"owner"`
			} `json:"repository"`
			Updates []*struct {
				Ref *struct {
					Name string `json:"name"`
				} `json:"ref"`
				Old *struct {
					ID string `json:"id"`
				} `json:"old"`
				New *struct {
					ID string `json:"id"`
				} `json:"new"`
			} `json:"updates"`
		} `json:"webhook"`
	} `json:"data"`
}

// Push reads a delivery the core has verified and returns what it claims. A
// delivery naming more than one ref is refused: a claim names one, and building
// one of several reads as the push handled.
func (d *signingDriver) Push(body forge.VerifiedBody, _ http.Header) (forge.PushClaim, error) {
	var p pushPayload
	if err := json.Unmarshal(body.Bytes(), &p); err != nil {
		return forge.PushClaim{}, fmt.Errorf("forge/sourcehut: unreadable push payload: %w", err)
	}
	w := p.Data.Webhook
	if w.Event != "GIT_POST_RECEIVE" {
		return forge.PushClaim{}, forge.ErrNotAPush
	}
	if w.Repository == nil {
		return forge.PushClaim{}, errors.New("forge/sourcehut: push payload names no repository")
	}
	c := &client{d: d.driver}
	key, err := c.keyOf(w.Repository.Owner.CanonicalName, w.Repository.Name, "a push payload's repository")
	if err != nil {
		return forge.PushClaim{}, errors.New("forge/sourcehut: push payload names no repository this layer can address")
	}
	switch {
	case len(w.Updates) == 0:
		return forge.PushClaim{}, forge.ErrNotAPush
	case len(w.Updates) > 1:
		return forge.PushClaim{}, fmt.Errorf("forge/sourcehut: the push updated %d refs and a claim names one", len(w.Updates))
	}
	u := w.Updates[0]
	if u == nil || u.Ref == nil {
		return forge.PushClaim{}, errors.New("forge/sourcehut: push payload names no ref")
	}
	var commit string
	switch {
	case u.New != nil:
		commit = u.New.ID
	case u.Old != nil:
		// A deletion: the ref has no new object. The core reads the all-zero
		// id, at the width the repository's hash has, as ErrRefDeleted.
		commit = strings.Repeat("0", len(u.Old.ID))
	}
	return forge.PushClaim{Repository: key, Ref: u.Ref.Name, Commit: commit}, nil
}
