package gitea

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// IsActiveMember answers with the deployment's token, never a caller's. Gitea
// answers 204 for a member and 404 otherwise; a token that may not list members
// is redirected to the public ones, which this driver does not follow, so it
// arrives as an error.
func (m *memberDriver) IsActiveMember(ctx context.Context, namespace, username string) (bool, error) {
	if namespace == "" || username == "" {
		return false, errors.New("forge/gitea: a membership question names a namespace and a username")
	}
	for what, half := range map[string]string{"namespace": namespace, "username": username} {
		if strings.Contains(half, "/") {
			return false, fmt.Errorf("forge/gitea: the %s %q nests, and Gitea addresses exactly one organization and one login", what, bounded(half))
		}
		if err := addressesItself(half); err != nil {
			return false, fmt.Errorf("forge/gitea: the %s of a membership question: %w", what, err)
		}
	}

	err := m.bind(m.member).send(ctx, routeMembership.at(namespace, username), nil, nil)
	switch {
	case err == nil:
		return true, nil
	case statusOf(err) == http.StatusNotFound:
		return false, nil
	}
	return false, fmt.Errorf("forge/gitea: membership of %q in %q could not be established: %w", username, namespace, err)
}
