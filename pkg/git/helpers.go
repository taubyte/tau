package git

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/google/go-github/v71/github"
	"golang.org/x/oauth2"
)

// ref: https://github.com/keybase/bot-sshca/blob/master/src/keybaseca/sshutils/generate.go#L53
func generateDeployKey() (string, string, error) {
	_privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}

	privateKey, err := x509.MarshalECPrivateKey(_privateKey)
	if err != nil {
		return "", "", err
	}

	privateKeyPEM := &pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKey}
	var private bytes.Buffer
	if err := pem.Encode(&private, privateKeyPEM); err != nil {
		return "", "", err
	}

	pub, err := ssh.NewPublicKey(&_privateKey.PublicKey)
	if err != nil {
		return "", "", err
	}

	return string(ssh.MarshalAuthorizedKey(pub)), private.String(), nil
}

func githubApiClient(ctx context.Context, token string) *github.Client {
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	return github.NewClient(tc)
}

func injectDeploymentKey(ctx context.Context, client *github.Client, user, repoName, name, key string) error {
	keys, _, err := client.Repositories.ListKeys(ctx, user, repoName, &github.ListOptions{})
	if err != nil {
		return err
	}
	for _, key := range keys {
		if key.GetTitle() == name {
			_, err = client.Repositories.DeleteKey(ctx, user, repoName, key.GetID())
			if err != nil {
				return err
			}
		}
	}
	_, _, err = client.Repositories.CreateKey(ctx, user, repoName, &github.Key{
		Title: &name,
		Key:   &key,
	})
	return err
}

// IsLocalURI reports whether url is a local:// URI (open existing dir, no clone).
func IsLocalURI(url string) bool {
	return strings.HasPrefix(url, LocalURIScheme)
}
