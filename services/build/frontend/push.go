package frontend

import (
	"fmt"
	"net/http"
)

// eligible reports whether a ref is one this cloud builds.
//
// THE POLICY IS HERE AND ONLY HERE (build.proto: "branch-eligibility policy is
// the FRONTEND's decision, made before Enqueue is ever called"). The backend
// validates a ref's SHAPE and does not re-filter it, so there is one list, in
// one config field, that an operator changes in one place. v1 read
// v1's legacy/specs/common.DefaultBranches — a package-level var in v1's frozen
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

// refusal is why a genuine push — from a verified delivery or a forge's event
// stream alike — is not built here, or nil. It is one function so that the two
// sources of a push cannot come to disagree about which pushes build.
func (s *Service) refusal(event Push) error {
	// Branch eligibility, before Enqueue and nowhere else.
	if !s.eligible(event.Ref) {
		return ignored("ref is not built")
	}

	// A REPOSITORY OUTSIDE THE TENANCY IS NOT BUILT, even when its push is
	// genuine: a hook registered before the tenancy was declared, or under
	// another, must not deploy here.
	if s.tenancy.Configured() {
		key, ok := parseRepositoryKey(event.Repository)
		err := fmt.Errorf("%q is not a repository key this cloud's tenancy can place", event.Repository)
		if ok {
			err = s.tenancy.Refuse(key)
		}
		if err != nil {
			return problem(http.StatusForbidden, err.Error())
		}
	}
	return nil
}
