package auth

import (
	"crypto/tls"
	"errors"

	"github.com/taubyte/tau/core/kvdb"
)

type Client interface {
	InjectStaticCertificate(domain string, data []byte) error
	GetCertificate(domain string) ([]byte, error)
	GetStaticCertificate(domain string) (*tls.Certificate, error)
	Hooks() Hooks
	Projects() Projects
	Repositories() Repositories
	Stats() Stats
}

type Stats interface {
	Database() (kvdb.Stats, error)
}

type Hook interface {
	Github() (*GithubHook, error)
	Bitbucket() (*BitbucketHook, error)
}

type Hooks interface {
	Get(hook_id string) (Hook, error)
	New(obj map[string]interface{}) (Hook, error)
	List() ([]string, error)
}

type Projects interface {
	New(obj map[string]interface{}) *Project
	Get(project_id string) *Project
	List() ([]string, error)
}

type Project struct {
	Client
	Id       string
	Name     string
	Provider string
	Git      struct {
		Config Repository
		Code   Repository
	}
}
type Repositories interface {
	Github() GithubRepositories
}

type GithubRepositories interface {
	New(obj map[string]interface{}) (GithubRepository, error)
	Get(id int) (GithubRepository, error)
	List() ([]string, error)
}

type Repository interface {
	PrivateKey() string
	Id() int
}

type BitbucketHook struct {
	Id string
}
type GithubHook struct {
	Id       string
	GithubId int
	Secret   string
}

type GithubRepository interface {
	Repository
	PrivateKey() string
	Project() string
}

func (h *GithubHook) Github() (*GithubHook, error) {
	return h, nil
}

func (h *GithubHook) Bitbucket() (*BitbucketHook, error) {
	return nil, errors.New("not a Bitbucket hook")
}
