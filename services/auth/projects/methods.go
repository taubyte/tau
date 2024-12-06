package projects

import (
	"context"
	"fmt"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/core/kvdb"
)

var (
	logger = log.Logger("tau.auth.service.api.projects")
)

func (r *ProjectObject) Serialize() Data {
	return Data{
		"id":       r.Id,
		"name":     r.Name,
		"provider": r.Provider,
		"code":     r.Code,
		"config":   r.Config,
	}
}

func (r *ProjectObject) Delete() error { return nil }

func (r *ProjectObject) Register() error { return nil }

func Exist(ctx context.Context, kv kvdb.KVDB, id string) bool {
	proj_name_key := fmt.Sprintf("/projects/%s/name", id)
	_, err := kv.Get(ctx, proj_name_key)
	return err == nil
}

// id: provided as string even if it's an int
func Fetch(ctx context.Context, kv kvdb.KVDB, id string) (Project, error) {
	logger.Debugf("Project.Fetch (%s)", id)
	proj_name_key := fmt.Sprintf("/projects/%s/name", id)
	name, err := kv.Get(ctx, proj_name_key)
	if err != nil {
		logger.Debugf("Project.Fetch (%s) -> key=%s (not found)", id, proj_name_key)
		return nil, fmt.Errorf("project `%s` not found", id)
	}

	provider, err := kv.Get(ctx, "/projects/"+id+"/repositories/provider")
	if err != nil {
		logger.Debugf("Project.Fetch (%s) -> key=%s (not found)", id, proj_name_key)
		return nil, fmt.Errorf("project `%s` not found", id)
	}

	configRepo, err := kv.Get(ctx, fmt.Sprintf("/projects/%s/repositories/config", id))
	if err != nil {
		logger.Debugf("Project.Fetch (%s) -> key=%s (not found)", id, proj_name_key)
		return nil, fmt.Errorf("project `%s` not found", id)
	}

	codeRepo, err := kv.Get(ctx, fmt.Sprintf("/projects/%s/repositories/code", id))
	if err != nil {
		logger.Debugf("Project.Fetch (%s) -> key=%s (not found)", id, proj_name_key)
		return nil, fmt.Errorf("project `%s` not found", id)
	}

	logger.Debugf("Project.Fetch (%s) -> FOUND", id)
	return New(kv, Data{
		"id":       id,
		"name":     string(name),
		"provider": provider,
		"code":     string(codeRepo),
		"config":   string(configRepo),
	})
}
