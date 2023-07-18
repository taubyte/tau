package service

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	authIface "github.com/taubyte/go-interfaces/services/auth"
	iface "github.com/taubyte/go-interfaces/services/patrick"
	authClient "github.com/taubyte/odo/clients/p2p/auth"
	"github.com/taubyte/utils/fs/dir"
	"golang.org/x/net/context"
)

type GitRepository struct {
	provider string
	id       string
	path     dir.Directory
	info     authIface.Repository
	key      *ssh.PublicKeys
	repo     *git.Repository
	logger   io.Writer
}

func (srv *PatrickService) NewGitRepository(provider string, repositoryId string, output io.Writer) (iface.GitRepository, error) {
	var (
		gr  GitRepository
		err error
	)

	gr.id = repositoryId
	gr.provider = provider
	gr.logger = output

	switch provider {
	case "github":
		id, err := strconv.Atoi(repositoryId)
		if err != nil {
			return nil, fmt.Errorf("failed str Atoi with error: %w", err)
		}

		repoInfo, err := srv.authClient.Repositories().Github().Get(id)
		if err != nil {
			return nil, fmt.Errorf("failed get repo's with error: %w", err)
		}

		gr.info = repoInfo

	default:
		gr.logger.Write([]byte("Error processing repository. `" + provider + "` not supported!"))
		return nil, errors.New("fnknown git provider")
	}

	gr.key, err = ssh.NewPublicKeys("git", []byte(gr.info.PrivateKey()), "")
	if err != nil {
		gr.logger.Write([]byte("Invalid git key. Error: " + err.Error()))
		return nil, errors.New("failed generating ssh key for git")
	}

	return &gr, nil
}

func (gr *GitRepository) Url() *string {
	switch gr.info.(type) {
	case *authClient.GithubRepository:
		return &(gr.info.(*authClient.GithubRepository).Url)
	default:
		return nil
	}
}

func (gr *GitRepository) Clone(ctx context.Context, path string, ref string) error {

	url := gr.Url()
	if url == nil {
		return errors.New("fepository does not have a URL")
	}

	var err error

	gr.path = dir.Directory(path)

	gr.repo, err = git.PlainCloneContext(ctx, gr.Path(), false, &git.CloneOptions{
		// The intended use of a GitHub personal access token is in replace of your password
		// because access tokens can easily be revoked.
		// https://help.github.com/articles/creating-a-personal-access-token-for-the-command-line/
		Auth:          gr.key,
		URL:           *url,
		Progress:      gr.logger,
		SingleBranch:  true,
		Depth:         1,
		ReferenceName: plumbing.ReferenceName(ref),
	})

	if err != nil {
		gr.logger.Write([]byte("Cloning failed with error: " + err.Error()))
		gr.path.Remove()
		gr.path = ""
	}

	return err

}

func (gr *GitRepository) Path() string {
	return gr.path.Path()
}
