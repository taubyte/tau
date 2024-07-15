package git

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/google/go-github/v32/github"
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

// TODO support other git providers
func embedGitToken(url string, auth transport.AuthMethod) (string, error) {
	if strings.Contains(url, "github.com") {
		basic, ok := auth.(*http.BasicAuth)
		if !ok {
			return "", fmt.Errorf("unsupported auth method: %T", auth)
		}

		return strings.Replace(url, "github.com", fmt.Sprintf("%s@github.com", basic.Password), 1), nil
	}

	return "", fmt.Errorf("unsupported url: %s", url)
}
