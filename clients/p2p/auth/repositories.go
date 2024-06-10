package auth

import (
	"fmt"

	iface "github.com/taubyte/tau/core/services/auth"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/utils/maps"
)

func (r RepositoryCommon) Id() int {
	return r.id
}

func (r *GithubRepository) PrivateKey() string {
	return r.Key
}

func (r *GithubRepository) Project() string {
	return r.project
}

func (c *Client) Repositories() iface.Repositories {
	return (*Repositories)(c)
}

func (r *Repositories) Github() iface.GithubRepositories {
	return (*GithubRepositories)(r)
}

func (r *GithubRepositories) New(obj map[string]interface{}) (iface.GithubRepository, error) {
	var repo GithubRepository
	var err error
	repo.project, _ = maps.String(obj, "project")

	repo.Name, _ = maps.String(obj, "name")

	repo.id, err = maps.Int(obj, "id")
	if err != nil {
		return nil, err
	}

	repo.Key, err = maps.String(obj, "key")
	if err != nil {
		return nil, err
	}

	repo.Url, _ = maps.String(obj, "url")

	return &repo, nil
}

func (r *GithubRepositories) Get(id int) (iface.GithubRepository, error) {
	logger.Debugf("Getting Github Repository `%d`", id)
	defer logger.Debugf("Getting Github Repository `%d` done", id)

	response, err := r.client.Send("repositories", command.Body{"action": "get", "provider": "github", "id": id}, r.peers...)
	if err != nil {
		return nil, err
	}

	return r.New(response)
}

func (r *GithubRepositories) List() ([]string, error) {
	response, err := r.client.Send("repositories", command.Body{"action": "list"}, r.peers...)
	if err != nil {
		return nil, err
	}
	ids, err := maps.StringArray(response, "ids")
	if err != nil {
		return nil, fmt.Errorf("failed map string array on list error: %v", err)
	}
	return ids, nil
}
