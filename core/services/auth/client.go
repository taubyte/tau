package auth

import (
	"context"
	"crypto/tls"
	"errors"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/core/kvdb"
)

type Client interface {
	InjectStaticCertificate(domain string, data []byte) error
	GetCertificate(domain string) (*tls.Certificate, error)
	GetStaticCertificate(domain string) (*tls.Certificate, error)
	GetRawCertificate(domain string) ([]byte, error)
	GetRawStaticCertificate(domain string) ([]byte, error)
	RegisterDomain(fqdn, projectID string) (*DomainRegistration, error)
	Hooks() Hooks
	Projects() Projects
	Repositories() Repositories
	Secrets() Secrets
	Stats() Stats // TODO: rename State
	Peers(...peerCore.ID) Client
	Close()
}

// Secrets provides client-side secret management operations
// Note: List() may have scalability concerns at large scale as it returns all secret IDs in memory.
// Consider pagination or streaming for future improvements.
type Secrets interface {
	// Store encrypts a secret client-side and stores it
	Store(ctx context.Context, secretID string, plaintext []byte) error
	// Retrieve retrieves and decrypts a secret
	Retrieve(ctx context.Context, secretID string) ([]byte, error)
	// Delete deletes a secret
	Delete(ctx context.Context, secretID string) error
	// Exists checks if a secret exists
	Exists(ctx context.Context, secretID string) (bool, error)
	// List lists all secret IDs
	List(ctx context.Context) ([]string, error)
	// PublicKeys returns available distributed keys with optional limit
	PublicKeys(ctx context.Context, opts ...PublicKeyOption) ([]DistributedKey, error)
}

type DomainRegistration struct {
	Token string `json:"token"`
	Entry string `json:"entry"`
	Type  string `json:"type"`
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
	Create(name, configRepoID, codeRepoID string) error
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
	Register(repoID string) (string, error)
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
