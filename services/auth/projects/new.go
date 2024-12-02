package projects

import (
	"strconv"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/services/auth"
	"github.com/taubyte/utils/maps"
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

	project := &ProjectObject{
		Id:       id,
		Name:     name,
		Provider: provider,
	}

	codeId, _ := maps.String(data, "code")
	var codeIdInt int
	if len(codeId) > 0 {
		codeIdInt, err = strconv.Atoi(codeId)
		if err != nil {
			codeIdInt = 0
		}
	}
	project.Code = codeIdInt

	configId, _ := maps.String(data, "config")
	var configIdInt int
	if len(configId) > 0 {
		configIdInt, err = strconv.Atoi(configId)
		if err != nil {
			configIdInt = 0
		}
	}
	project.Config = configIdInt

	return project, nil

}
