package dream

import (
	"errors"
	"slices"
	"testing"

	authIface "github.com/taubyte/tau/core/services/auth"
	tnsIface "github.com/taubyte/tau/core/services/tns"
	"gotest.tools/v3/assert"
)

type fakeAuth struct {
	authIface.Client
	projects map[int]string
}

func (a fakeAuth) Repositories() authIface.Repositories { return a }
func (a fakeAuth) Github() authIface.GithubRepositories { return a }

func (a fakeAuth) Get(id int) (authIface.GithubRepository, error) {
	project, ok := a.projects[id]
	if !ok {
		return nil, errors.New("not registered")
	}
	return fakeRepo{project: project}, nil
}

func (fakeAuth) New(map[string]interface{}) (authIface.GithubRepository, error) { return nil, nil }
func (fakeAuth) List() ([]string, error)                                        { return nil, nil }
func (fakeAuth) Register(string) (string, error)                                { return "", nil }

type fakeRepo struct {
	authIface.GithubRepository
	project string
}

func (r fakeRepo) Project() string { return r.project }

type fakeTns struct {
	tnsIface.Client
	projects map[string]string
}

func (t fakeTns) Simple() tnsIface.SimpleIface { return t }

func (fakeTns) Commit(string, ...string) (string, string, error) { return "", "", nil }
func (fakeTns) Project(string, ...string) (interface{}, error)   { return nil, nil }
func (t fakeTns) GetRepositoryProjectId(_, repoId string) (string, error) {
	if p, ok := t.projects[repoId]; ok {
		return p, nil
	}
	return "", errors.New("not found")
}

func TestRepoProject(t *testing.T) {
	auth := fakeAuth{projects: map[int]string{1: "QmA", 2: ""}}
	tns := fakeTns{projects: map[string]string{"2": "QmB", "3": "QmC"}}

	assert.Equal(t, repoProject(auth, tns, "1"), "QmA", "auth knows it")
	assert.Equal(t, repoProject(auth, tns, "2"), "QmB", "auth has no project for it, TNS does")
	assert.Equal(t, repoProject(auth, tns, "3"), "QmC", "only TNS knows it")
	assert.Equal(t, repoProject(auth, tns, "4"), "", "nobody knows it: pushed anyway")
}

func TestRepoKindOrder(t *testing.T) {
	names := []string{
		"me/tb_website_shop",
		"me/tb_code_shop",
		"me/tb_library_utils",
		"me/tb_shop",
	}
	slices.SortStableFunc(names, func(a, b string) int { return repoKindOrder(a) - repoKindOrder(b) })
	assert.DeepEqual(t, names, []string{"me/tb_shop", "me/tb_library_utils", "me/tb_code_shop", "me/tb_website_shop"})
}
