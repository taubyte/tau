package gerrit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// IsActiveMember answers whether username belongs to the Gerrit group named
// namespace, with the deployment's credential. Two requests: a member 404 is the
// same for a non-member and an invisible group, so the group is fetched first.
// Gerrit resolves identifiers loosely, so the group and member that answered
// must carry the names asked about. A missing account is (false, nil).
func (m *memberDriver) IsActiveMember(ctx context.Context, namespace, username string) (bool, error) {
	if namespace == "" || username == "" {
		return false, errors.New("forge/gerrit: a membership question names a group and a username")
	}
	if username == "self" || strings.Trim(username, "0123456789") == "" {
		return false, fmt.Errorf("forge/gerrit: %q is an account id to Gerrit, not a username", bounded(username))
	}
	c := m.bind(m.member)

	var g struct {
		Name string `json:"name"`
	}
	if err := c.send(ctx, routeGroup.at(namespace), nil, &g); err != nil {
		return false, fmt.Errorf("forge/gerrit: membership of %q in %q could not be established: %w", bounded(username), bounded(namespace), err)
	}
	if g.Name != namespace {
		return false, fmt.Errorf("forge/gerrit: asked about the group %q and %s answered with another group", bounded(namespace), m.host)
	}

	var a struct {
		Username string `json:"username"`
	}
	err := c.send(ctx, routeGroupMember.at(namespace, username), nil, &a)
	switch {
	case statusOf(err) == http.StatusNotFound:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("forge/gerrit: membership of %q in %q could not be established: %w", bounded(username), bounded(namespace), err)
	case !strings.EqualFold(a.Username, username):
		return false, fmt.Errorf("forge/gerrit: asked whether %q is in %q and %s answered about another account", bounded(username), bounded(namespace), m.host)
	}
	return true, nil
}
