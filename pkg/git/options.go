package git

import (
	"fmt"

	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	gossh "golang.org/x/crypto/ssh"
)

type Option func(c *Repository) error

/* URL is an Option to set the repository URL.
 *
 * url: The URL to use.
 */
func URL(url string) Option {
	return func(c *Repository) error {
		c.url = url
		return nil
	}
}

/* Token is an Option to set the repository token.
 *
 * token: The token to use.
 */
func Token(token string) Option {
	return func(c *Repository) error {
		c.auth = &http.BasicAuth{
			Username: fakeUserforTokenAuth,
			Password: token,
		}
		return nil
	}
}

/* EmbeddedToken is an Option to set the repository token.
 * the token will also be embedded in the remote url.
 *
 * token: The token to use.
 */
func EmbeddedToken(token string) Option {
	return func(c *Repository) error {
		c.embedToken = true
		c.auth = &http.BasicAuth{
			Username: fakeUserforTokenAuth,
			Password: token,
		}

		return nil
	}
}

/* SSHKey is an Option to set the repository SSH key.
 *
 * key: The key to use.
 */
func SSHKey(key string) Option {
	return func(c *Repository) error {
		auth, err := ssh.NewPublicKeys("git", []byte(key), "")
		if err != nil {
			return fmt.Errorf("adding SSH Key failed with %w", err)
		}
		// need to bypass checking ssh host keys
		// see: https://github.com/src-d/go-git/issues/637#issuecomment-404851019
		auth.HostKeyCallbackHelper = ssh.HostKeyCallbackHelper{
			HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		}
		c.auth = auth
		return nil
	}
}

/* Author is an Option to set the repository author.
 *
 * username: The username to use.
 * email: The email to use.
 */
func Author(username, email string) Option {
	return func(c *Repository) error {
		c.user.name = username
		c.user.email = email
		return nil
	}
}

/* Temporary is an Option to set the repository to be temporary.
 *
 * Returns error if something goes wrong.
 */
func Temporary() Option {
	return func(c *Repository) error {
		c.ephemeral = true
		return nil
	}
}

/* Preserve is an Option to set the repository to be preserved.
 * For use with Temporary to keep the tmp/repo-* directory alive
 *
 * Returns error if something goes wrong.
 */
func Preserve() Option {
	return func(c *Repository) error {
		c.ephemeralNoDelete = true
		return nil
	}
}

/* Root is an Option to set the repository root.
 *
 * root: The root to use.
 */
func Root(root string) Option {
	return func(c *Repository) error {
		c.root = root
		return nil
	}
}

/* Branch is an Option to set the repository branch.
 *
 * branch: The branch to use.
 */
func Branch(branch string) Option {
	return func(c *Repository) error {
		c.branches = []string{branch}
		c.usingSpecificBranch = true
		return nil
	}
}
