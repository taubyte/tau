package onedev

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// repositoryHeader is the custom header every hook this driver registers
// carries: "<project id> <clone url>", written at registration from what the
// forge itself answered for the hooked project. OneDev replays a hook's custom
// headers verbatim on every delivery, beside the token. It is how a delivery
// names its repository at all — see the package comment.
const repositoryHeader = "X-Tau-Repository"

// Scheme is what this driver registers hooks with. It implements nothing:
// X-OneDev-Signature is the stored secret replayed verbatim, and that
// procedure is forge.SchemeOneDevToken, in the core, beside its vectors.
func (d *driver) Scheme() forge.Scheme { return forge.SchemeOneDevToken }

// pushPayload is the whole of what this driver reads out of a delivery, which
// is OneDev's RefUpdated event serialised as it is: a project NUMBER, the ref,
// and the two commits. The user ids and the date are absent here on purpose.
type pushPayload struct {
	Type        string `json:"type"`
	ProjectID   int64  `json:"projectId"`
	RefName     string `json:"refName"`
	NewCommitID string `json:"newCommitId"`
}

// Push reads a delivery the core HAS ALREADY VERIFIED and returns what it
// CLAIMS; the core compares that claim with the hook's own key.
//
// THE CLAIM COMES FROM TWO HALVES THAT MUST AGREE. The payload says which
// project was pushed, by number; the repository header says which number this
// hook was registered for and which clone URL that number had. Only when the
// two numbers are the same does the header's clone URL name the push. They
// differ on every push to a DESCENDANT of the hooked project, because OneDev
// delivers a project's hooks for its whole subtree — and that delivery is
// refused here rather than filed under its ancestor, which is the one answer
// that would build the wrong repository's code.
//
// Both halves arrive under the same unbound token, so a holder of the token
// can say anything in either; that is the scheme's ceiling, the reason the zero
// Policy refuses it, and not something a driver can raise.
func (d *driver) Push(body forge.VerifiedBody, header http.Header) (forge.PushClaim, error) {
	var p pushPayload
	if err := json.Unmarshal(body.Bytes(), &p); err != nil {
		return forge.PushClaim{}, fmt.Errorf("forge/onedev: unreadable delivery: %w", err)
	}
	// OneDev names the event in the body, not in a header. A hook this driver
	// registers subscribes to pushes alone, but a hook is editable in the UI.
	if p.Type != "RefUpdated" {
		return forge.PushClaim{}, forge.ErrNotAPush
	}

	value := header.Get(repositoryHeader)
	if value == "" {
		return forge.PushClaim{}, fmt.Errorf("forge/onedev: delivery carries no %s header, so it names no repository; the hook was not registered by this driver", repositoryHeader)
	}
	if len(value) > maxHostString+32 {
		return forge.PushClaim{}, fmt.Errorf("forge/onedev: the %s header is implausibly long", repositoryHeader)
	}
	idText, cloneURL, ok := strings.Cut(value, " ")
	hooked, err := strconv.ParseInt(idText, 10, 64)
	if !ok || err != nil || hooked <= 0 {
		return forge.PushClaim{}, fmt.Errorf("forge/onedev: the %s header is not \"<project id> <clone url>\"", repositoryHeader)
	}
	if p.ProjectID != hooked {
		return forge.PushClaim{}, fmt.Errorf("forge/onedev: delivery is a push to project %d through a hook registered on project %d; OneDev delivers a hook for the hooked project's descendants too, and a descendant is not the repository this hook is for", p.ProjectID, hooked)
	}
	// The reason is not carried, the gitea driver's rule: repokey quotes what it
	// was handed, and a clone URL with an identity half would be a credential in
	// a tau log.
	key, err := repokey.Parse(cloneURL)
	if err != nil {
		return forge.PushClaim{}, errors.New("forge/onedev: delivery names no repository this layer can address")
	}
	return forge.PushClaim{Repository: key, Ref: p.RefName, Commit: p.NewCommitID}, nil
}
