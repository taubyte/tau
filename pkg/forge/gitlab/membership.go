package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// IsActiveMember answers with the deployment's credential, which no Session can
// reach. A 404 from the members endpoint is the only definitive non-member;
// anything else is an error. Two round trips: the endpoints take a user id,
// resolved per call.
func (d *memberDriver) IsActiveMember(ctx context.Context, namespace, username string) (bool, error) {
	if namespace == "" || username == "" {
		return false, fmt.Errorf("gitlab: a membership question needs both a group and a username")
	}
	// A login is one segment and a group is not: a group's full path holds "/" as
	// data, escaped to %2F.
	if strings.Contains(username, "/") {
		return false, fmt.Errorf("gitlab: the username %q nests, and a login is one path segment", bounded(username))
	}
	// Dot segments are refused here, in both halves: at the route a bad group would
	// be refused only after the user lookup spent the deployment's credential.
	for what, half := range map[string]string{"group": namespace, "username": username} {
		if err := addressesItself(half); err != nil {
			return false, fmt.Errorf("gitlab: the %s of a membership question: %w", what, err)
		}
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
	r, err := do(ctx, d.api, d.cfg, routeGroupMember.at(namespace, strconv.FormatInt(id, 10)), nil)
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
	rq := routeUser.at().with(url.Values{"username": {username}})

	r, err := do(ctx, d.api, d.cfg, rq, nil)
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
