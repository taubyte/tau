package frontend

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// push is everything this role reads out of a provider's push payload: the
// three fields build.proto's PushEvent carries, and nothing else.
//
// WHAT IS DELIBERATELY NOT READ, on service-topology.md §5's D5 ruling: the
// per-commit `commits[].added|modified|removed` lists (provider-shaped, and
// truncated past 20 commits or 3000 files by GitHub itself, so a path filter
// fed from them is wrong in exactly the cases that matter), `before`,
// `pushed_at`, `head_commit`, every clone/ssh/git/html URL beyond the one this
// file parses into a key, and the numeric repository id. The backend derives
// its own before..after anchor from its own history; anything else crossing
// this boundary would be a stranger's bytes deciding what the private half
// builds.
type push struct {
	Repository repokey.Key
	Ref        string
	CommitSHA  string
}

// zeroSHA is what git and GitHub both write for "no commit": a branch that was
// just created has it as `before`, and a branch that was just DELETED has it as
// `after`.
const zeroSHA = "0000000000000000000000000000000000000000"

// parsePush turns a verified body into a push.
//
// It is a pure function of the bytes — no clock, no network, no service — which
// is what lets the payload cases be tested without a server, and what keeps the
// provider's shape confined to one file. A second provider is a second function
// here, not a change anywhere else.
func parsePush(body []byte) (push, error) {
	var payload struct {
		Ref        string `json:"ref"`
		After      string `json:"after"`
		Deleted    bool   `json:"deleted"`
		Repository struct {
			CloneURL string `json:"clone_url"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return push{}, problem(http.StatusBadRequest, "malformed payload")
	}

	// A DELETED BRANCH IS NOT A BUILD, and it is the one payload shape that
	// would otherwise sail through every check below it: `after` is forty zeros,
	// which is a perfectly well-formed lowercase object name, so the backend's
	// own validation would accept it and queue a build of a ref that no longer
	// exists. Refusing it is provider knowledge, so it is refused here.
	if payload.Deleted || payload.After == zeroSHA {
		return push{}, ignored("ref deleted")
	}

	// The repository is named by ITS OWN clone url, parsed into pkg/git/repokey's
	// canonical key. That is the one string that crosses to the backend, and it
	// is derived rather than trusted: two spellings of one repository would be
	// two job ids for one push, and the backend's idempotency — and therefore
	// this cloud's answer to a redelivered webhook — rests on there being one.
	if payload.Repository.CloneURL == "" {
		return push{}, problem(http.StatusBadRequest, "payload names no repository")
	}
	key, err := repokey.Parse(payload.Repository.CloneURL)
	if err != nil {
		return push{}, problem(http.StatusBadRequest, "payload names no repository")
	}

	if !strings.HasPrefix(payload.Ref, "refs/") {
		return push{}, problem(http.StatusBadRequest, "payload names no ref")
	}
	if payload.After == "" {
		return push{}, problem(http.StatusBadRequest, "payload names no commit")
	}

	return push{Repository: key, Ref: payload.Ref, CommitSHA: payload.After}, nil
}

// eligible reports whether a ref is one this cloud builds.
//
// THE POLICY IS HERE AND ONLY HERE (build.proto: "branch-eligibility policy is
// the FRONTEND's decision, made before Enqueue is ever called"). The backend
// validates a ref's SHAPE and does not re-filter it, so there is one list, in
// one config field, that an operator changes in one place. v1 read
// legacy/specs/common.DefaultBranches — a package-level var in v1's frozen
// address space — and then exempted itself from it entirely in dev mode, so
// the policy differed between the cloud an author tested on and the one they
// deployed to.
func (s *Service) eligible(ref string) bool {
	for _, allowed := range s.cfg.EligibleRefs {
		if ref == allowed {
			return true
		}
	}
	return false
}

// ignored is the answer to a push this cloud does not build: accepted, because
// the delivery was genuine and the provider should not retry it, and unbuilt.
// The reason reaches the caller in X-Build-Ignored, where the provider's own
// delivery log shows it — this is a signed, genuine delivery, so saying why
// costs nothing and is the only place an author finds out.
func ignored(reason string) error { return problem(http.StatusAccepted, reason) }
