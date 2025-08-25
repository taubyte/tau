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

func (r *projectObject) Serialize() Data {
	return Data{
		"id":       r.id,
		"name":     r.name,
		"provider": r.provider,
		"code":     r.code,
		"config":   r.config,
	}
}

func (r *projectObject) Delete() error {
	ctx := context.Background()

	// Create a batch for atomic operations
	batch, err := r.kv.Batch(ctx)
	if err != nil {
		return fmt.Errorf("failed to create batch: %w", err)
	}

	// Batch all project deletion operations
	err = batch.Delete(fmt.Sprintf("/projects/%s/name", r.id))
	if err != nil {
		return fmt.Errorf("failed to batch delete project name: %w", err)
	}

	err = batch.Delete(fmt.Sprintf("/projects/%s/repositories/provider", r.id))
	if err != nil {
		return fmt.Errorf("failed to batch delete project provider: %w", err)
	}

	err = batch.Delete(fmt.Sprintf("/projects/%s/repositories/config", r.id))
	if err != nil {
		return fmt.Errorf("failed to batch delete project config repository: %w", err)
	}

	err = batch.Delete(fmt.Sprintf("/projects/%s/repositories/code", r.id))
	if err != nil {
		return fmt.Errorf("failed to batch delete project code repository: %w", err)
	}

	// Commit all deletions atomically
	err = batch.Commit()
	if err != nil {
		return fmt.Errorf("failed to commit project deletion batch: %w", err)
	}

	return nil
}

func (r *projectObject) Register() error {
	ctx := context.Background()

	// Create a batch for atomic operations
	batch, err := r.kv.Batch(ctx)
	if err != nil {
		return fmt.Errorf("failed to create batch: %w", err)
	}

	// Batch all project data operations
	err = batch.Put(fmt.Sprintf("/projects/%s/name", r.id), []byte(r.name))
	if err != nil {
		return fmt.Errorf("failed to batch project name: %w", err)
	}

	err = batch.Put(fmt.Sprintf("/projects/%s/repositories/provider", r.id), []byte(r.provider))
	if err != nil {
		return fmt.Errorf("failed to batch project provider: %w", err)
	}

	err = batch.Put(fmt.Sprintf("/projects/%s/repositories/config", r.id), []byte(r.config))
	if err != nil {
		return fmt.Errorf("failed to batch project config repository: %w", err)
	}

	err = batch.Put(fmt.Sprintf("/projects/%s/repositories/code", r.id), []byte(r.code))
	if err != nil {
		return fmt.Errorf("failed to batch project code repository: %w", err)
	}

	// Commit all operations atomically
	err = batch.Commit()
	if err != nil {
		return fmt.Errorf("failed to commit project registration batch: %w", err)
	}

	return nil
}

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
