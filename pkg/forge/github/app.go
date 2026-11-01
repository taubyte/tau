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

// app is the deployment's credential, baked into the driver, so no
// caller-bound Session can reach it.
type app struct {
	clientID string
	key      *rsa.PrivateKey
}

// App gives the driver the deployment's GitHub App credential, which answers
// membership. An unusable key fails here at startup, not mid-registration where
// it would look like "not a member". The PEM is never quoted in an error.
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

// IsActiveMember answers with the deployment's app credential, since GitHub
// answers 404 to a user token for both "not a member" and "you may not look":
// find the installation, mint a token scoped to members:read, and ask. Only a
// 404 on the membership itself is (false, nil); a 403 means the namespace
// blocked the app. Nothing is cached: a revocation window is the consumer's
// policy.
func (m *memberDriver) IsActiveMember(ctx context.Context, namespace, username string) (bool, error) {
	if namespace == "" || username == "" {
		return false, errors.New("forge/github: a membership question names a namespace and a username")
	}
	// Each half is one path segment: a "/" would fill one hole with two segments.
	for what, half := range map[string]string{"namespace": namespace, "username": username} {
		if strings.Contains(half, "/") {
			return false, fmt.Errorf("forge/github: the %s %q nests, and GitHub addresses exactly one organization and one login", what, bounded(half))
		}
		// Dot segments are refused here: at the route a "." reaches the refusal only on
		// the third request, after the mint hands back a credential.
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
