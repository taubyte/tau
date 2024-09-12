package helpers

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/google/go-github/github"
	"github.com/taubyte/tau/dream/helpers"
	"github.com/taubyte/tau/pkg/git"
	"golang.org/x/crypto/ssh"
	"golang.org/x/oauth2"
)

func CloneToDirSSH(ctx context.Context, dir string, _repo helpers.Repository) (err error) {
	pubKey, secKey, err := generateDeployKey()
	if err != nil {
		return
	}

	githubClient := githubApiClient(ctx, helpers.GitToken)

	err = injectDeploymentKey(ctx, githubClient, helpers.GitUser, _repo.Name, "go-simple-git-clone-with-deploy-key", pubKey)
	if err != nil {
		return
	}

	gitOptions := []git.Option{
		git.URL(_repo.HookInfo.Repository.SSHURL),
		git.SSHKey(secKey),
		git.Root(dir),
	}

	if _repo.HookInfo.Repository.Branch != "" {
		gitOptions = append(gitOptions, git.Branch(_repo.HookInfo.Repository.Branch))
	}

	// clone repo
	_, err = git.New(ctx, gitOptions...)
	if err != nil {
		return
	}

	repo, _, err := githubClient.Repositories.Get(ctx, helpers.GitUser, _repo.Name)
	if err != nil {
		return
	}
	if repo.ID == nil {
		err = fmt.Errorf("repo ID not found")
		return
	}
	return
}

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
