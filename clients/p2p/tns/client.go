package p2p

import (
	"context"
	"fmt"

	"github.com/mitchellh/mapstructure"
	"github.com/taubyte/go-interfaces/moody"
	"github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/odo/clients/p2p/tns/common"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/p2p/streams/client"
	"github.com/taubyte/p2p/streams/command"

	spec "github.com/taubyte/go-specs/common"
	"github.com/taubyte/go-specs/extract"
	"github.com/taubyte/go-specs/methods"
	"github.com/taubyte/utils/maps"
)

func New(ctx context.Context, node *peer.Node) (tns.Client, error) {
	var (
		c   Client
		err error
	)

	c.cache = newCache(node)
	c.node = node
	c.client, err = client.New(ctx, node, nil, spec.TnsProtocol, MinPeers, MaxPeers)
	if err != nil {
		logger.Error(moody.Object{"message": fmt.Sprintf("API client creation failed: %s", err.Error())})
		return nil, err
	}

	logger.Debug(moody.Object{"message": "API client Created!"})
	return &c, nil
}

func (c *Client) Close() {
	c.cache.close()
	c.client.Close()
}

/****** LIST *******/
func (c *Client) List(depth int) ([]string, error) {
	response, err := c.client.Send("list", command.Body{"depth": depth})
	if err != nil {
		logger.Error(moody.Object{"error": err.Error()})
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
	logger.Debug(moody.Object{"message": fmt.Sprintf("Fetching keys %v", path.String())})
	defer logger.Debug(moody.Object{"message": fmt.Sprintf("Fetching keys %v DONE", path.String())})

	var err error
	object := c.cache.get(path)
	if object == nil {
		object, err = c.fetch(path.Slice())
		if err != nil {
			return nil, err
		}
		c.cache.put(path, object)
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
	logger.Debug(moody.Object{"message": fmt.Sprintf("Fetching prefixes %v/%v", query, query)})
	defer logger.Debug(moody.Object{"message": fmt.Sprintf("Fetching prefixes %v/%v DONE", query, query)})

	return c.lookup(query)
}

/****** PUSH *******/

func (c *Client) Push(path []string, data interface{}) error {
	logger.Debug(moody.Object{"message": fmt.Sprintf("Pushing object at %s", path)})
	defer logger.Debug(moody.Object{"message": fmt.Sprintf("Pushing keys %s DONE", path)})

	response, err := c.client.Send("push", command.Body{
		"path": path,
		"data": data,
	})
	if err != nil {
		logger.Error(moody.Object{"message": fmt.Sprintf("push failed with: %s", err.Error())})
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
	response, err := c.client.Send("fetch", command.Body{"path": path})
	if err != nil {
		logger.Error(moody.Object{"message": fmt.Sprintf("fetch failed with: %s", err.Error())})
		return nil, err
	}

	obj, ok := response["object"]
	if !ok {
		return nil, fmt.Errorf("no object found for %s", path)
	}

	return obj, nil
}

func (c *Client) lookup(query tns.Query) ([]string, error) {
	response, err := c.client.Send("lookup", command.Body{"prefix": query.Prefix, "regex": query.RegEx})
	if err != nil {
		logger.Error(moody.Object{"message": fmt.Sprintf("lookup failed with: %s", err.Error())})
		return nil, err
	}

	return maps.StringArray(response, "keys")
}

type responseObject struct {
	object interface{}
	path   tns.Path
	tns    *Client
}

// Use for indexed object links
func (r *responseObject) Current(branch string) ([]tns.Path, error) {
	// Grab Interface and convert to list
	ifaceList, ok := r.Interface().([]interface{})
	if !ok {
		return nil, fmt.Errorf("cannot convert paths iface `%v` to []interface{}", r.Interface())
	}

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

	if len(paths) < 1 {
		return nil, fmt.Errorf("no paths returned from current for branch %s", branch)
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
