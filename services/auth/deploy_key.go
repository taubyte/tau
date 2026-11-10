package auth

import (
	"fmt"

	"github.com/google/go-github/v71/github"
	"golang.org/x/crypto/ssh"
)

const (
	deployKeyName    = "taubyte_deploy_key"
	devDeployKeyName = "taubyte_deploy_key_dev"
)

// deployKeyTitle names the deploy keys this cloud adds. Registering a
// repository removes its other keys of the same title, so each cloud needs its
// own: two clouds building one repository must not delete each other's key.
// Dream keeps one title for all its universes, being one machine's cloud.
func deployKeyTitle(devMode bool, fqdn string) string {
	if devMode {
		return devDeployKeyName
	}
	if fqdn == "" {
		return deployKeyName
	}
	return deployKeyName + "_" + fqdn
}

// RemoveDeployKey deletes the selected repository's deploy key equal to key (an
// authorized_keys line), whatever its title; a key that is not there is no
// error. It goes by value because a title proves nothing: every release before
// per-cloud titles used deployKeyName, so another cloud's key can carry it too.
func (client *githubClient) RemoveDeployKey(key string) error {
	owner, repo, err := client.currentOwnerRepo()
	if err != nil {
		return err
	}
	want := sameDeployKey(key)

	opts := &github.ListOptions{PerPage: 100}
	for {
		keys, resp, err := client.Repositories.ListKeys(client.ctx, owner, repo, opts)
		if err != nil {
			return fmt.Errorf("listing deploy keys of %s/%s failed with: %w", owner, repo, err)
		}
		for _, k := range keys {
			if sameDeployKey(k.GetKey()) != want {
				continue
			}
			if _, err := client.Repositories.DeleteKey(client.ctx, owner, repo, k.GetID()); err != nil {
				return fmt.Errorf("deleting deploy key %d of %s/%s failed with: %w", k.GetID(), owner, repo, err)
			}
			return nil
		}
		if resp == nil || resp.NextPage == 0 {
			return nil
		}
		opts.Page = resp.NextPage
	}
}

// removeReplacedKey deletes from GitHub the deploy key whose private half was
// stored until now. It is this cloud's own and no longer used by anything,
// whatever its title: a repository last registered on an older release holds a
// key those releases wrote to the logs.
func removeReplacedKey(client GitHubClient, private []byte) error {
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil {
		return fmt.Errorf("parsing the replaced key failed with: %w", err)
	}
	return client.RemoveDeployKey(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
}
