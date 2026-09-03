package github

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// appJWTTTL is under GitHub's ten-minute ceiling for an app assertion.
	appJWTTTL = 9 * time.Minute

	// appJWTBackdate absorbs clock skew between this process and GitHub, which
	// rejects an assertion issued in its own future.
	appJWTBackdate = 30 * time.Second
)

// app is THE DEPLOYMENT'S credential, and where it lives is the point. It is
// baked into the driver at construction, so a caller-bound Session has no path
// to it and it has no path to acting as a caller. That is structural rather
// than a rule somebody has to keep.
type app struct {
	clientID string
	key      *rsa.PrivateKey
}

// App gives this driver the deployment's GitHub App credential, which is what
// makes it able to answer a membership question at all.
//
// IT FAILS HERE, AT STARTUP, ON AN UNUSABLE KEY, rather than deferring the
// failure to whichever registration first needs it — an unreadable key
// discovered then is an outage in the middle of an interactive flow, and it is
// indistinguishable there from "this user is not a member".
//
// The PEM is never quoted in an error.
func App(clientID string, pemKey []byte) Option {
	return func(d *driver) error {
		if clientID == "" {
			return errors.New("forge/github: an app credential needs its client id")
		}
		key, err := jwt.ParseRSAPrivateKeyFromPEM(pemKey)
		if err != nil {
			return fmt.Errorf("forge/github: the app private key is unusable: %w", err)
		}
		d.app = &app{clientID: clientID, key: key}
		return nil
	}
}

// assertion is the app's own JWT, which is good for exactly two things:
// finding the app's installations and minting their tokens.
func (a *app) assertion(now time.Time) (string, error) {
	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-appJWTBackdate)),
		ExpiresAt: jwt.NewNumericDate(now.Add(appJWTTTL)),
		Issuer:    a.clientID,
	}).SignedString(a.key)
	if err != nil {
		return "", fmt.Errorf("forge/github: signing the app assertion failed: %w", err)
	}
	return signed, nil
}

type installationJSON struct {
	ID int64 `json:"id"`
}

type installationTokenRequest struct {
	Permissions map[string]string `json:"permissions"`
}

type installationTokenJSON struct {
	Token string `json:"token"`
}

type membershipJSON struct {
	State string `json:"state"`
}

// IsActiveMember answers the one question a caller's own token cannot.
//
// A user token carries whatever scopes that user happened to grant, and GitHub
// answers 404 for both "not a member" and "you may not look" — which are not
// the same answer, and a gate built on the second one is a gate that opens for
// whoever asks with a narrow token. So this is answered with the DEPLOYMENT's
// app credential, over three requests: find the installation on the namespace,
// mint a token for it scoped to members:read and nothing else, and ask.
//
// THE CONTRACT IS v1'S, UNCHANGED. (false, nil) is a definitive non-member —
// only a 404 on the membership itself, and only after the first two requests
// succeeded — and everything else is an error. A 403 means the namespace
// blocked the app, which is an operator problem and not a verdict on this user;
// answering false there would deny a legitimate member because of a setting
// they do not control.
//
// NOTHING IS CACHED HERE. v1 caches both the installation token and the verdict,
// and the argument for either TTL is a revocation-window argument that belongs
// to whoever owns the gate — so it stays with the consumer, and this driver
// costs two extra requests on a rare interactive path instead of holding a
// staleness policy it cannot justify.
func (m *memberDriver) IsActiveMember(ctx context.Context, namespace, username string) (bool, error) {
	if namespace == "" || username == "" {
		return false, errors.New("forge/github: a membership question names a namespace and a username")
	}
	// EACH HALF IS ONE PATH SEGMENT. Addressable already refuses a key whose
	// owner nests, with the sentence "GitHub addresses exactly one owner and one
	// name"; an organization login and a user login are the same shape, and a
	// "/" in either would fill one hole of orgs/{}/memberships/{} with two
	// segments — a question asked about a route nobody named. It is a SHAPE rule
	// and it lives here, where the shape is known; the dot-segment rule below
	// belongs to every hole of every route and is stated at both ends for the
	// reason given there.
	for what, half := range map[string]string{"namespace": namespace, "username": username} {
		if strings.Contains(half, "/") {
			return false, fmt.Errorf("forge/github: the %s %q nests, and GitHub addresses exactly one organization and one login", what, bounded(half))
		}
		// AND THE DOT SEGMENTS, HERE RATHER THAN AT THE ROUTE. The route refuses
		// them too, and for this operation that is too late: measured, a
		// username of "." reached the route's refusal only on the THIRD request,
		// after the installation lookup and after the mint — the one request tau
		// makes that hands back a credential. A question that cannot be asked
		// costs nothing here and costs an installation token there.
		if err := addressesItself(half); err != nil {
			return false, fmt.Errorf("forge/github: the %s of a membership question: %w", what, err)
		}
	}

	assertion, err := m.app.assertion(time.Now())
	if err != nil {
		return false, err
	}
	appClient := m.bind(assertion)

	var install installationJSON
	if err := appClient.send(ctx, routeInstallation.at(namespace), nil, &install); err != nil {
		return false, fmt.Errorf("forge/github: this deployment's app could not be found on %q: %w", namespace, err)
	}
	if install.ID == 0 {
		return false, fmt.Errorf("forge/github: the installation on %q has no id", namespace)
	}

	var minted installationTokenJSON
	// The narrowest token the question needs, and not everything the app holds.
	body := installationTokenRequest{Permissions: map[string]string{"members": "read"}}
	if err := appClient.send(ctx, routeInstallationToken.at(strconv.FormatInt(install.ID, 10)), body, &minted); err != nil {
		return false, fmt.Errorf("forge/github: minting an installation token for %q: %w", namespace, err)
	}
	if minted.Token == "" {
		return false, fmt.Errorf("forge/github: the installation token minted for %q is empty", namespace)
	}

	var membership membershipJSON
	if err := m.bind(minted.Token).send(ctx, routeMembership.at(namespace, username), nil, &membership); err != nil {
		if statusOf(err) == http.StatusNotFound {
			return false, nil
		}
		return false, fmt.Errorf("forge/github: membership of %q in %q could not be established: %w", username, namespace, err)
	}
	// "pending" is an invitation nobody accepted, which is not membership.
	return membership.State == "active", nil
}
