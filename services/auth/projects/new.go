package projects

import (
	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/services/auth"
	"github.com/taubyte/tau/utils/maps"
)

type Repository struct {
	auth.Repository
	id int
}

func (r Repository) Id() int {
	return r.id
}

func New(kv kvdb.KVDB, data Data) (Project, error) {
	id, err := maps.String(data, "id")
	if err != nil {
		return nil, err
	}

	name, err := maps.String(data, "name")
	if err != nil {
		return nil, err
	}

	provider, err := maps.String(data, "provider")
	if err != nil {
		provider = "github"
	}

	project := &projectObject{
		id:       id,
		name:     name,
		provider: provider,
		kv:       kv,
	}

	codeId, _ := maps.String(data, "code")
	project.code = codeId

	configId, _ := maps.String(data, "config")
	project.config = configId

	return project, nil
}
