package tns

import (
	"context"
	"fmt"

	"github.com/mitchellh/mapstructure"
	"github.com/taubyte/tau/clients/p2p/tns/common"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"

	spec "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/extract"
	"github.com/taubyte/tau/pkg/specs/methods"
	"github.com/taubyte/utils/maps"

	srvCommon "github.com/taubyte/tau/services/common"
)

func New(ctx context.Context, node peer.Node) (tns.Client, error) {
	var (
		c   Client
		err error
	)

	c.cache = newCache(node)
	c.node = node
	c.client, err = client.New(node, srvCommon.TnsProtocol)
	if err != nil {
		logger.Error("API client creation failed:", err)
		return nil, err
	}

	logger.Debug("API client Created!")
	return &c, nil
}

func (c *Client) Close() {
	c.cache.close()
	c.client.Close()
}

/****** LIST *******/
func (c *Client) List(depth int) ([]string, error) {
	response, err := c.client.Send("list", command.Body{"depth": depth}, c.peers...)
	if err != nil {
		logger.Error(err)
		return nil, err
	}

	keys, err := maps.StringArray(response, "keys")
	if err != nil {
		return nil, fmt.Errorf("failed string array in list with error: %v", err)
	}

	return keys, nil
}

/****** FETCH *******/
// Fetch a key, does not watch nor cache value
func (c *Client) Fetch(path tns.Path) (tns.Object, error) {
	logger.Debugf("Fetching keys %v", path.String())
	defer logger.Debugf("Fetching keys %v DONE", path.String())

	var err error
	object := c.cache.get(path)
	if object == nil {
		object, err = c.fetch(path.Slice())
		if err != nil {
			return nil, err
		}
		c.cache.put(path, object) // this will start a go func to watch FIX
	}

	return &responseObject{
		path:   path,
		object: object,
		tns:    c,
	}, nil
}

/****** FETCH *******/
// Fetch a key, does not watch nor cache value
func (c *Client) Lookup(query tns.Query) (interface{}, error) {
	logger.Debugf("Fetching prefixes %v/%v", query, query)
	defer logger.Debugf("Fetching prefixes %v/%v DONE", query, query)

	return c.lookup(query)
}

/****** PUSH *******/

func (c *Client) Push(path []string, data interface{}) error {
	logger.Debugf("Pushing object at %s", path)
	defer logger.Debugf("Pushing keys %s DONE", path)

	response, err := c.client.Send("push", command.Body{
		"path": path,
		"data": data,
	}, c.peers...)
	if err != nil {
		logger.Error("push failed with:", err)
		return err
	}
	_pushed, ok := response["pushed"]
	pushed, pushed_type_ok := _pushed.(bool)
	if pushed && ok && pushed_type_ok {
		if len(path) >= 2 {
			if err := c.node.PubSubPublish(c.client.Context(), common.GetChannelFor(path...), nil); err != nil {
				return fmt.Errorf("push failed to publish: %s", err.Error())
			}
		}

		return nil
	}

	return fmt.Errorf("failed to push %v", path)
}

/****** COMMON *******/

func (c *Client) fetch(path []string) (interface{}, error) {
	response, err := c.client.Send("fetch", command.Body{"path": path}, c.peers...)
	if err != nil {
		logger.Error("fetch failed with:", err)
		return nil, err
	}

	obj, ok := response["object"]
	if !ok {
		return nil, fmt.Errorf("no object found for %s", path)
	}

	return obj, nil
}

func (c *Client) lookup(query tns.Query) ([]string, error) {
	response, err := c.client.Send("lookup", command.Body{"prefix": query.Prefix, "regex": query.RegEx}, c.peers...)
	if err != nil {
		logger.Error("lookup failed with:", err)
		return nil, err
	}

	return maps.StringArray(response, "keys")
}

// Use for indexed object links
func (r *responseObject) Current(branches []string) (paths []tns.Path, err error) {
	ifaceList, ok := r.Interface().([]interface{})
	if !ok {
		return nil, fmt.Errorf("cannot convert paths iface `%v` to []interface{}", r.Interface())
	}

	for _, branch := range branches {
		paths, err = r.current(ifaceList, branch)
		if err == nil && len(paths) != 0 {
			break
		}
	}

	if len(paths) == 0 {
		return nil, fmt.Errorf("no paths returned from current for branches %s", branches)
	}

	return
}

func (r *responseObject) current(ifaceList []interface{}, branch string) ([]tns.Path, error) {
	paths := make([]tns.Path, 0)
	var projectId string
	var commit string
	for i, _pathIface := range ifaceList {
		path, ok := _pathIface.(string)
		if !ok {
			return nil, fmt.Errorf("cannot convert path iface  `%v` to string", _pathIface)
		}
		extractPath, err := extract.Tns().BasicPath(path)
		if err != nil {
			return nil, err
		}

		if i == 0 {
			projectId = extractPath.Project()
			commitObj, err := r.tns.Fetch(spec.Current(projectId, branch))
			if err != nil {
				return nil, fmt.Errorf("fetching current commit for project `%s` on branch `%s` failed with: %w", projectId, branch, err)
			}

			commit, ok = commitObj.Interface().(string)
			if !ok {
				return nil, fmt.Errorf("cannot convert commit iface `%v` to string", commitObj)
			}

		}
		if extractPath.Project() != projectId {
			return nil, fmt.Errorf("unexpected project ID `%s` in index! ", extractPath.Project())
		}

		currentPath, err := methods.GetBasicTNSKey(extractPath.Branch(), commit, projectId, extractPath.Application(), extractPath.Resource(), spec.PathVariable(extractPath.ResourceType()))
		if err != nil {
			return nil, fmt.Errorf("getting basic tns key for index `%s` failed with: %w", path, err)
		}

		paths = append(paths, currentPath)
	}

	return paths, nil
}

func (r *responseObject) Path() tns.Path {
	return r.path
}

func (r *responseObject) Bind(structure interface{}) error {
	return mapstructure.Decode(r.object, structure)
}

func (r *responseObject) Interface() interface{} {
	switch r.object.(type) {
	case *responseObject:
		return r.object.(*responseObject).Interface()
	case []*responseObject:
		vals := r.object.([]*responseObject)
		ret := make([]interface{}, len(vals))
		for i, v := range vals {
			ret[i] = v.Interface()
		}
		return ret
	default:
		return r.object
	}
}
