package context

import (
	"encoding/json"
	"fmt"

	"github.com/taubyte/utils/maps"
)

func (c *Context) ParseBody(obj interface{}) error {
	return json.Unmarshal(c.body, obj)
}

func (c *Context) GetStringMapVariable(key string) (map[string]interface{}, error) {
	obj, ok := c.variables[key]
	if !ok {
		return nil, fmt.Errorf("can't find variable %s of type map", key)
	}

	return maps.SafeInterfaceToStringKeys(obj), nil
}

func (c *Context) GetStringArrayVariable(key string) ([]string, error) {
	return maps.StringArray(c.variables, key)
}

func (c *Context) GetStringVariable(key string) (string, error) {
	return maps.String(c.variables, key)
}

func (c *Context) GetIntVariable(key string) (int, error) {
	return maps.Int(c.variables, key)
}
