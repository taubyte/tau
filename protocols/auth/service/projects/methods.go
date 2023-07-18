package projects

import (
	"context"
	"fmt"

	logging "github.com/ipfs/go-log/v2"
	"github.com/taubyte/go-interfaces/kvdb"
)

var (
	logger = logging.Logger("auth.service.api.projects")
)

func (r *ProjectObject) Serialize() Data {
	return Data{
		"id":     r.Id,
		"name":   r.Name,
		"code":   r.Code,
		"config": r.Config,
	}
}

func (r *ProjectObject) Delete() error {

	return nil
}

func (r *ProjectObject) Register() error {

	return nil
}

func Exist(ctx context.Context, kv kvdb.KVDB, id string) bool {
	proj_name_key := fmt.Sprintf("/projects/%s/name", id)
	_, err := kv.Get(ctx, proj_name_key)
	return err == nil
}

func fetchGithub(ctx context.Context, kv kvdb.KVDB, id int) (Project, error) {
	repo_key := fmt.Sprintf("/projects/github/%d", id)

	// name, err := kv.Get(repo_key + "/name")
	// if err != nil {
	// 	return nil, err
	// }

	// project, err := kv.Get(repo_key + "/project")
	// if err != nil {
	// 	return nil, err
	// }

	key, err := kv.Get(ctx, repo_key+"/key")
	if err != nil {
		return nil, err
	}

	// url, err := kv.Get(repo_key + "/url")
	// if err != nil {
	// 	return nil, err
	// }

	return New(kv, Data{
		"id": id,
		//"name":     string(name),
		"provider": "github",
		//"project":  string(project),
		"key": string(key),
		//"url":      string(url),
	})

}

// id: provided as string even if it's an int
func Fetch(ctx context.Context, kv kvdb.KVDB, id string) (Project, error) {
	logger.Debug(fmt.Sprintf("Project.Fetch (%s)", id))
	proj_name_key := fmt.Sprintf("/projects/%s/name", id)
	name, err := kv.Get(ctx, proj_name_key)
	if err != nil {
		logger.Debug(fmt.Sprintf("Project.Fetch (%s) -> key=%s (not found)", id, proj_name_key))
		return nil, fmt.Errorf("project `%s` not found", id)
	}

	configRepo, err := kv.Get(ctx, fmt.Sprintf("/projects/%s/repositories/config", id))
	if err != nil {
		logger.Debug(fmt.Sprintf("Project.Fetch (%s) -> key=%s (not found)", id, proj_name_key))
		return nil, fmt.Errorf("project `%s` not found", id)
	}

	codeRepo, err := kv.Get(ctx, fmt.Sprintf("/projects/%s/repositories/code", id))
	if err != nil {
		logger.Debug(fmt.Sprintf("Project.Fetch (%s) -> key=%s (not found)", id, proj_name_key))
		return nil, fmt.Errorf("project `%s` not found", id)
	}

	logger.Debug(fmt.Sprintf("Project.Fetch (%s) -> FOUND", id))
	return New(kv, Data{
		"id":     id,
		"name":   string(name),
		"code":   string(codeRepo),
		"config": string(configRepo),
	})
}
