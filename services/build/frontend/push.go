package frontend

import (
	"fmt"
	"net/http"
)

// eligible reports whether a ref is one this cloud builds; the only place the
// policy lives.
func (s *Service) eligible(ref string) bool {
	for _, allowed := range s.cfg.EligibleRefs {
		if ref == allowed {
			return true
		}
	}
	return false
}

// ignored answers a genuine push this cloud does not build: accepted so the
// provider does not retry, with the reason in X-Build-Ignored.
func ignored(reason string) error { return problem(http.StatusAccepted, reason) }

// refusal is why a genuine push, delivered or streamed, is not built here, or
// nil.
func (s *Service) refusal(event Push) error {
	// Branch eligibility, before Enqueue and nowhere else.
	if !s.eligible(event.Ref) {
		return ignored("ref is not built")
	}

	// A repository outside the tenancy is not built, even with a genuine push.
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
