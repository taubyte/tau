package engine

import (
	"bytes"
	"context"
	"fmt"

	"github.com/taubyte/tau/protocols/tns/flat"
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

func (e *Engine) executeOps(ctx context.Context, ops []op) (err error) {
	rev := make([]op, 0)
	for _, o := range ops {
		rev = append([]op{o}, rev...)
		err = o.Execute(ctx)
		if err != nil {
			break
		}
	}
	if err != nil {
		for _, ro := range rev {
			err = ro.Revert(ctx)
			if err != nil {
				return fmt.Errorf("op revert failed with: %s", err)
			}
		}
	}
	return
}
