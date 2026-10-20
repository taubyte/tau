package gerrit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// IsActiveMember answers whether username belongs to the Gerrit GROUP named
// namespace, with the DEPLOYMENT's credential and never a caller's. A group is
// what Gerrit has where other forges have an organization: it owns projects and
// it has members, and nothing else in Gerrit has both.
//
// IT IS TWO REQUESTS, because Gerrit's one-request answer is ambiguous. GET
// /groups/{g}/members/{a} is 404 for a non-member AND for a group the
// credential cannot see (measured on 3.12.3: both answer 404), so reading that
// 404 as "not a member" would turn "we could not look" into a verdict. The
// group is fetched first; only once it is known to be visible is a 404 on the
// member a definitive (false, nil).
//
// AND GERRIT RESOLVES IDENTIFIERS LOOSELY. A group id may be a UUID, a legacy
// number or a name; an account id may be "self", a number, an email, a
// username or a full name. So the answer is checked against the question: the
// group that answered must carry the name asked about, and the member that
// answered must carry the username asked about, or the answer was about
// somebody else and is an error. A 404 on an account that does not exist is
// still (false, nil): nobody is not a member.
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
