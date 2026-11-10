package engine

import (
	"bytes"
	"context"
	"fmt"

	"github.com/taubyte/tau/services/tns/flat"
)

func (e *Engine) Merge(ctx context.Context, object *flat.Object) error {
	keys, err := e.db.List(ctx, keyFromPath(append(Prefix, object.Root...)))
	if err != nil {
		return err
	}

	org := make(map[string][]byte)
	for _, k := range keys {
		data, err := e.db.Get(ctx, k)
		if err == nil && data != nil {
			org[k] = data
		}
	}

	return e.mergeObject(ctx, object, org)
}

func (e *Engine) mergeObject(ctx context.Context, object *flat.Object, org map[string][]byte) error {
	ops, err := e.generateOps(ctx, object, org)
	if err != nil {
		return err
	}

	return e.executeOps(ctx, ops)
}

type op interface {
	Execute(ctx context.Context) error
	Revert(ctx context.Context) error
}

type kvop struct {
	engine  *Engine
	key     string
	orgData []byte
	curData []byte
}

func (o *kvop) Execute(ctx context.Context) error {
	if o.curData == nil {
		return o.engine.db.Delete(ctx, o.key)
	} else {
		return o.engine.db.Put(ctx, o.key, o.curData)
	}
}

func (o *kvop) Revert(ctx context.Context) error {
	if o.orgData != nil {
		return o.engine.db.Put(ctx, o.key, o.orgData)
	} else {
		return o.engine.db.Delete(ctx, o.key)
	}
}

func (e *Engine) generateOps(ctx context.Context, object *flat.Object, org map[string][]byte) ([]op, error) {
	ops := make([]op, 0)
	for _, item := range object.Data {
		key := keyFromPath(append(object.Root, item.Path...))
		data := org[key]
		cur, err := encode(item.Data)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(cur, data) {
			ops = append(ops, &kvop{
				engine:  e,
				key:     key,
				orgData: data,
				curData: cur,
			})
		}
		// if exists in org delete so we're left with
		// a list of keys to delete from DB
		delete(org, key)
	}
	for key, data := range org {
		ops = append(ops, &kvop{
			engine:  e,
			key:     key,
			orgData: data,
			curData: nil,
		})
	}
	return ops, nil
}

// executeOps writes a merge as one batch: all of it or none of it, and one
// change for the other nodes to replicate. Written key by key, a config push
// (hundreds of keys) reached the other nodes over several 5 s rebroadcasts,
// and a new website was served 30–40 s late there.
func (e *Engine) executeOps(ctx context.Context, ops []op) error {
	if len(ops) == 0 {
		return nil
	}

	batch, err := e.db.Batch(ctx)
	if err != nil {
		return fmt.Errorf("starting batch failed with: %w", err)
	}

	for _, o := range ops {
		kv, ok := o.(*kvop)
		if !ok {
			return fmt.Errorf("unexpected op %T", o)
		}
		if kv.curData == nil {
			err = batch.Delete(kv.key)
		} else {
			err = batch.Put(kv.key, kv.curData)
		}
		if err != nil {
			return fmt.Errorf("batching `%s` failed with: %w", kv.key, err)
		}
	}

	if err = batch.Commit(); err != nil {
		return fmt.Errorf("committing %d keys failed with: %w", len(ops), err)
	}
	return nil
}
