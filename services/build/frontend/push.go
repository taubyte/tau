package frontend

import "net/http"

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
