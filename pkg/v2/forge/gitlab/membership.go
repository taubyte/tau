package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// IsActiveMember reports whether a named human is an active member of a GitLab
// group.
//
// IT IS ANSWERED WITH THE DEPLOYMENT'S CREDENTIAL, which lives on the driver and
// which no Session can reach — that is the placement, and it is structural
// rather than a rule. A caller's own token carries whatever scopes that caller
// granted, and a 404 from it conflates "not a member" with "you may not look",
// so the question would be unanswerable from a Session no matter how it were
// spelled.
//
// (false, nil) IS THE ONLY DEFINITIVE NON-MEMBER. A 404 from the members
// endpoint is that answer; everything else — a 403 because the token was
// blocked, a 500, an outage — is an error, because a caller acts on a false and
// an operator problem must not read as a verdict about a person.
//
// It is two round trips because GitLab's membership endpoints take a USER ID and
// tau knows a username. That id is resolved per call and stored nowhere.
func (d *memberDriver) IsActiveMember(ctx context.Context, namespace, username string) (bool, error) {
	if namespace == "" || username == "" {
		return false, fmt.Errorf("gitlab: a membership question needs both a group and a username")
	}

	id, found, err := d.userID(ctx, username)
	if err != nil {
		return false, err
	}
	if !found {
		// No such user on this instance. That is a definitive non-member: there
		// is nobody for the group to contain.
		return false, nil
	}

	// members/all rather than members: it includes membership INHERITED from an
	// ancestor group, which is how most people are members of a subgroup, and
	// omitting it would answer false for someone who plainly is one.
	u := d.cfg.endpoint("api", "v4", "groups", namespace, "members", "all", strconv.FormatInt(id, 10))
	r, err := do(ctx, d.api, d.cfg.host, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	if r.status == http.StatusNotFound {
		return false, nil
	}
	if err := fault(d.cfg.host, r, nil); err != nil {
		return false, err
	}

	var member struct {
		AccessLevel int    `json:"access_level"`
		State       string `json:"state"`
	}
	if err := decode(r, &member); err != nil {
		return false, err
	}
	// A member awaiting approval, or one whose access level is No access, is
	// listed and is not a member for any purpose tau has.
	return member.AccessLevel > 0 && (member.State == "" || member.State == "active"), nil
}

// userID resolves a username to the id GitLab's membership endpoints take. An
// empty answer is "no such user" and not a failure.
func (d *memberDriver) userID(ctx context.Context, username string) (int64, bool, error) {
	u := d.cfg.endpoint("api", "v4", "users")
	u.RawQuery = url.Values{"username": {username}}.Encode()

	r, err := do(ctx, d.api, d.cfg.host, http.MethodGet, u, nil)
	if err != nil {
		return 0, false, err
	}
	if err := fault(d.cfg.host, r, nil); err != nil {
		return 0, false, err
	}
	var users []struct {
		ID    int64  `json:"id"`
		State string `json:"state"`
	}
	if err := decode(r, &users); err != nil {
		return 0, false, err
	}
	for _, user := range users {
		if user.ID == 0 {
			continue
		}
		// A blocked or deactivated account is not an active member of anything.
		if user.State != "" && user.State != "active" {
			return 0, false, nil
		}
		return user.ID, true, nil
	}
	return 0, false, nil
}
