package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/taubyte/tau/services/tns/flat"
)

func (e *Engine) match(ctx context.Context, path ...string) ([]string, error) {
	prefix := keyFromPath(path)
	c, err := e.db.ListAsync(ctx, keyFromPath(path[:len(path)-1]))
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0)
	for p := range c {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			keys = append(keys, p)
		}
	}

	return keys, nil
}

func (e *Engine) Get(ctx context.Context, path ...string) (*flat.Object, error) {
	object := flat.Empty(path)
	keys, err := e.match(ctx, path...)
	if err != nil {
		return nil, err
	}

	for _, k := range keys {
		relativePath, err := relativePathFromKey(path, k)
		if err != nil {
			return nil, err
		}
		data, err := e.db.Get(ctx, k)
		if err != nil {
			return nil, fmt.Errorf("Get failed: %v", err)
		}

		var decoded interface{}
		err = decode(data, &decoded)
		if err != nil {
			return nil, fmt.Errorf("decode failed: %v", err)
		}
		if decoded != nil {
			object.Data = append(object.Data, flat.Item{
				Path: relativePath,
				Data: decoded,
			})
		}
	}
	if err != nil {
		return nil, err
	}
	return object, nil
}
