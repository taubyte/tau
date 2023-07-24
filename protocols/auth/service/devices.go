package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"

	crypto "github.com/libp2p/go-libp2p/core/crypto"
	peer "github.com/libp2p/go-libp2p/core/peer"

	http "github.com/taubyte/http"
)

func (srv *AuthService) getDeviceEnvFromDatabase(ctx context.Context, project_id string, device_id string) map[string]string {

	project_path := fmt.Sprintf("/project/%s", project_id)
	devices_path := fmt.Sprintf("%s/devices", project_path)
	device_path := fmt.Sprintf("%s/%s", devices_path, device_id)

	devices_envs, err := srv.db.List(ctx, device_path+"/env/")
	if err != nil {
		return map[string]string{}
	}

	re := regexp.MustCompile("^/.+/([^/]+)$")

	fmt.Println("ENV: ", devices_envs)
	env := make(map[string]string)
	for _, _env := range devices_envs {
		m := re.FindStringSubmatch(_env)
		if len(m) > 1 {
			val, _ := srv.db.Get(ctx, _env)
			env[m[1]] = string(val)
		}
	}

	return env

}

func (srv *AuthService) getDeviceTagsFromDatabase(ctx context.Context, project_id string, device_id string) []string {

	project_path := fmt.Sprintf("/project/%s", project_id)
	devices_path := fmt.Sprintf("%s/devices", project_path)
	device_path := fmt.Sprintf("%s/%s", devices_path, device_id)

	devices_tags, err := srv.db.List(ctx, device_path+"/tags/")
	if err != nil {
		return []string{}
	}

	re := regexp.MustCompile("^/.+/([^/]+)$")

	fmt.Println("TAGS: ", devices_tags)
	tags := make([]string, 0)
	for _, _tag := range devices_tags {
		m := re.FindStringSubmatch(_tag)
		if len(m) > 1 {
			tags = append(tags, m[1])
		}
	}

	return tags

}

func (srv *AuthService) newDeviceID() (id peer.ID, publicKey []byte, privateKey []byte, err error) {
	priv, pub, err := crypto.GenerateKeyPair(crypto.Ed25519, 1)

	if err != nil {
		return /*"",*/ "", nil, nil, err
	}
	_priv, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return /*"",*/ "", nil, nil, err
	}

	_pub, err := crypto.MarshalPublicKey(pub)
	if err != nil {
		return /*"",*/ "", nil, nil, err
	}

	_id, err := peer.IDFromPublicKey(pub)
	if err != nil {
		return /*"",*/ "", nil, nil, err
	}

	return _id, _pub, _priv, nil

}

func (srv *AuthService) newDeviceHTTPHandler(ctx http.Context) (interface{}, error) {
	projectid, err := ctx.GetStringVariable("projectid")
	if err != nil {
		return nil, err
	}

	device_name, err := ctx.GetStringVariable("name")
	if err != nil {
		return nil, err
	}

	device_type, err := ctx.GetStringVariable("type")
	if err != nil {
		return nil, err
	}

	device_description, err := ctx.GetStringVariable("description")
	if err != nil {
		return nil, err
	}

	tags, err := ctx.GetStringArrayVariable("tags")
	if err != nil {
		return nil, err
	}

	env, err := ctx.GetStringMapVariable("env")
	if err != nil {
		return nil, err
	}

	// checks

	if len(device_name) < 3 || !isStringVarName(device_name) {
		return nil, errors.New("device name not correct")
	}

	for _, tag := range tags {
		if len(tag) < 3 || !isStringVarName(tag) {
			return nil, errors.New("incorrect tag format")
		}
	}

	// lets do it

	project_path := fmt.Sprintf("/project/%s", projectid)

	devices_path := fmt.Sprintf("%s/devices", project_path)

	_device_peer_id, device_pub, device_private, err := srv.newDeviceID()
	if err != nil {
		return nil, err
	}

	device_peer_id := fmt.Sprint(_device_peer_id)

	device_path := fmt.Sprintf("%s/%s", devices_path, device_peer_id)

	requestCtx := ctx.Request().Context()

	// TODO: add some clean-up logic if it fails
	srv.db.Put(requestCtx, device_path+"/name", []byte(device_name))
	srv.db.Put(requestCtx, device_path+"/type", []byte(device_type))
	srv.db.Put(requestCtx, device_path+"/description", []byte(device_description))

	for _, tag := range tags {
		srv.db.Put(requestCtx, device_path+"/tags/"+tag, []byte{1})
	}

	for ev_name, ev_value := range env {
		val, ok := ev_value.(string)
		if !ok {
			return nil, fmt.Errorf("env variable %s is not a string", ev_name)
		}
		srv.db.Put(requestCtx, device_path+"/env/"+ev_name, []byte(val))
	}

	srv.db.Put(requestCtx, device_path+"/key", device_pub)
	srv.db.Put(requestCtx, device_path+"/enabled", []byte{1})

	return map[string]interface{}{
		"id":         device_peer_id,
		"publicKey":  base64.StdEncoding.EncodeToString(device_pub),
		"privateKey": base64.StdEncoding.EncodeToString(device_private),
	}, nil
}

func (srv *AuthService) getDevicesHTTPHandler(ctx http.Context) (interface{}, error) {
	projectid, err := ctx.GetStringVariable("projectid")
	if err != nil {
		return nil, err
	}

	project_path := fmt.Sprintf("/project/%s", projectid)
	devices_path := fmt.Sprintf("%s/devices/", project_path)

	requestCtx := ctx.Request().Context()
	_devices, err := srv.db.List(requestCtx, devices_path)

	if err != nil {
		return nil, err
	}

	re := regexp.MustCompile(devices_path + "([^/]+)/name")

	devices := make([]string, 0)

	for _, dev := range _devices {
		m := re.FindStringSubmatch(dev)
		if len(m) > 1 {
			devices = append(devices, m[1])
		}
	}

	fmt.Println("devices:", devices)

	return map[string]interface{}{"devices": devices}, nil
}

func (srv *AuthService) getDeviceHTTPHandler(ctx http.Context) (interface{}, error) {
	projectid, err := ctx.GetStringVariable("projectid")
	if err != nil {
		return nil, err
	}

	device_id, err := ctx.GetStringVariable("deviceid")
	if err != nil {
		return nil, err
	}

	project_path := fmt.Sprintf("/project/%s", projectid)

	devices_path := fmt.Sprintf("%s/devices", project_path)

	device_path := fmt.Sprintf("%s/%s", devices_path, device_id)

	requestCtx := ctx.Request().Context()
	// TODO: add some clean-up logic if it fails
	// TODO: add error handling
	device_name, err := srv.db.Get(requestCtx, device_path+"/name")
	if err != nil {
		return nil, err
	}

	device_pub, err := srv.db.Get(requestCtx, device_path+"/key")
	if err != nil {
		return nil, err
	}

	device_enabled, _ := srv.db.Get(requestCtx, device_path+"/enabled")
	if err != nil {
		device_enabled = []byte{0}
	}

	device_description, err := srv.db.Get(requestCtx, device_path+"/description")
	if err != nil {
		device_description = []byte{}
	}

	device_type, err := srv.db.Get(requestCtx, device_path+"/type")
	if err != nil {
		device_type = []byte{}
	}

	tags := srv.getDeviceTagsFromDatabase(requestCtx, projectid, device_id)

	env := srv.getDeviceEnvFromDatabase(requestCtx, projectid, device_id)

	return map[string]interface{}{
		"id":          device_id,
		"name":        string(device_name),
		"description": string(device_description),
		"type":        string(device_type),
		"tags":        tags,
		"env":         env,
		"publicKey":   base64.StdEncoding.EncodeToString(device_pub),
		"enabled":     int(device_enabled[0]) == 1,
	}, nil

}

func (srv *AuthService) editDeviceHTTPHandler(ctx http.Context) (interface{}, error) {
	projectid, err := ctx.GetStringVariable("projectid")
	if err != nil {
		return nil, err
	}

	device_id, err := ctx.GetStringVariable("deviceid")
	if err != nil {
		return nil, err
	}

	project_path := fmt.Sprintf("/project/%s", projectid)

	devices_path := fmt.Sprintf("%s/devices", project_path)

	device_path := fmt.Sprintf("%s/%s", devices_path, device_id)

	device_type, err := ctx.GetStringVariable("type")
	if err != nil {
		return nil, err
	}

	device_description, err := ctx.GetStringVariable("description")
	if err != nil {
		return nil, err
	}

	device_name, err := ctx.GetStringVariable("name")
	if err != nil {
		return nil, err
	}

	tags, err := ctx.GetStringArrayVariable("tags")
	if err != nil {
		return nil, err
	}

	env, err := ctx.GetStringMapVariable("env")
	if err != nil {
		return nil, err
	}

	// run check
	if len(device_name) < 3 || !isStringVarName(device_name) {
		return nil, errors.New("device name not correct")
	}

	for _, tag := range tags {
		if len(tag) < 3 || !isStringVarName(tag) {
			return nil, errors.New("incorrect tag format")
		}
	}

	requestCtx := ctx.Request().Context()
	devices_envs, err := srv.db.List(requestCtx, device_path+"/env/")
	if err == nil && len(devices_envs) > 0 {
		for _, t := range devices_envs {
			srv.db.Delete(requestCtx, t) // TODO: add bulk (or transaction concept) delete to kvdb
		}
	}

	for ev_name, ev_value := range env {
		val, ok := ev_value.(string)
		if !ok {
			return nil, fmt.Errorf("env variable %s is not a string", ev_name)
		}
		srv.db.Put(requestCtx, device_path+"/env/"+ev_name, []byte(val))
	}

	// lets do it

	// TODO: add some clean-up logic if it fails
	srv.db.Put(requestCtx, device_path+"/name", []byte(device_name))
	srv.db.Put(requestCtx, device_path+"/type", []byte(device_type))
	srv.db.Put(requestCtx, device_path+"/description", []byte(device_description))

	// ------

	devices_tags, err := srv.db.List(requestCtx, device_path+"/tags/")
	if err == nil && len(devices_tags) > 0 {
		for _, t := range devices_tags {
			srv.db.Delete(requestCtx, t) // TODO: add bulk (or transaction concept) delete to kvdb
		}
	}

	for _, tag := range tags {
		srv.db.Put(requestCtx, device_path+"/tags/"+tag, []byte{1})
	}

	// ------

	return map[string]interface{}{
		"id": device_id,
	}, nil
}

func (srv *AuthService) setDeviceState(ctx context.Context, projectid string, device_id string, enabled bool) error {

	project_path := fmt.Sprintf("/project/%s", projectid)

	devices_path := fmt.Sprintf("%s/devices", project_path)

	device_path := fmt.Sprintf("%s/%s", devices_path, device_id)

	_state := []byte{0}
	if enabled {
		_state = []byte{1}
	}

	err := srv.db.Put(ctx, device_path+"/enabled", _state)
	if err != nil {
		return errors.New("can't change state")
	}

	return nil
}

func (srv *AuthService) setDevicesState(ctx context.Context, projectid string, ids []string, enabled bool) (map[string]interface{}, error) {
	_ids := make([]string, 0)
	error_ids := make(map[string]string)

	for _, id := range ids {
		err := srv.setDeviceState(ctx, projectid, id, enabled)
		if err == nil {
			_ids = append(_ids, id)
		} else {
			error_ids[id] = err.Error()
		}
	}

	return map[string]interface{}{
		"changed": _ids,
		"errors":  error_ids,
	}, nil
}

func (srv *AuthService) enableDeviceHTTPHandler(ctx http.Context) (interface{}, error) {
	projectid, err := ctx.GetStringVariable("projectid")
	if err != nil {
		return nil, err
	}

	ids, err := ctx.GetStringArrayVariable("ids")
	if err != nil {
		return nil, err
	}

	return srv.setDevicesState(ctx.Request().Context(), projectid, ids, true)
}

func (srv *AuthService) disableDeviceHTTPHandler(ctx http.Context) (interface{}, error) {
	projectid, err := ctx.GetStringVariable("projectid")
	if err != nil {
		return nil, err
	}

	ids, err := ctx.GetStringArrayVariable("ids")
	if err != nil {
		return nil, err
	}

	return srv.setDevicesState(ctx.Request().Context(), projectid, ids, false)
}

func (srv *AuthService) delDeviceHTTPHandler(ctx http.Context) (interface{}, error) {
	projectid, err := ctx.GetStringVariable("projectid")
	if err != nil {
		return nil, err
	}

	ids, err := ctx.GetStringArrayVariable("ids")
	if err != nil {
		return nil, err
	}

	project_path := fmt.Sprintf("/project/%s", projectid)

	devices_path := fmt.Sprintf("%s/devices/", project_path)

	deleted_ids := make([]string, 0)
	error_ids := make(map[string]string)

	requestCtx := ctx.Request().Context()
	for _, id := range ids {
		device_entries, err := srv.db.List(requestCtx, devices_path+id)
		fmt.Println("Deleteing: ", device_entries)
		if err == nil && len(device_entries) > 0 {
			for _, p := range device_entries {
				srv.db.Delete(requestCtx, p) // TODO: add bulk (or transaction concept) delete to kvdb
			}
			deleted_ids = append(deleted_ids, id)
		} else {
			if err == nil {
				error_ids[id] = "Device does not exist"
			} else {
				error_ids[id] = "Deleting device resulting in error `" + err.Error() + "`"
			}
		}
	}

	return map[string]interface{}{
		"deleted": deleted_ids,
		"errors":  error_ids,
	}, nil
}

func (srv *AuthService) setupDevicesHTTPRoutes() {
	host := ""
	if len(srv.hostUrl) > 0 {
		host = "auth.tau." + srv.hostUrl
	}

	//srv.http.POST("/project/{projectid}/devices/new", []string{"projectid", "name", "tags", "description", "type", "env"}, []string{"devices/all", "devices/new"}, srv.GitHubTokenHTTPAuth, srv.newDeviceHTTPHandler, srv.GitHubTokenHTTPAuthCleanup)
	srv.http.POST(&http.RouteDefinition{
		Host: host,
		Path: "/project/{projectid}/devices/new",
		Vars: http.Variables{
			Required: []string{"projectid", "name", "tags", "description", "type", "env"},
		},
		Scope: []string{"devices/all", "devices/new"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.newDeviceHTTPHandler,
	})
	//srv.http.GET("/project/{projectid}/devices", []string{"projectid"}, []string{"devices/all", "devices/list"}, srv.GitHubTokenHTTPAuth, srv.getDevicesHTTPHandler, srv.GitHubTokenHTTPAuthCleanup)
	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/project/{projectid}/devices",
		Vars: http.Variables{
			Required: []string{"projectid"},
		},
		Scope: []string{"devices/all", "devices/list"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.getDevicesHTTPHandler,
	})
	//srv.http.GET("/project/{projectid}/device/{deviceid}", []string{"projectid", "deviceid"}, []string{"devices/all", "devices/view"}, srv.GitHubTokenHTTPAuth, srv.getDeviceHTTPHandler, srv.GitHubTokenHTTPAuthCleanup)
	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/project/{projectid}/device/{deviceid}",
		Vars: http.Variables{
			Required: []string{"projectid", "deviceid"},
		},
		Scope: []string{"devices/all", "devices/view"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.getDeviceHTTPHandler,
	})
	//srv.http.PUT("/project/{projectid}/device/{deviceid}", []string{"projectid", "deviceid", "tags", "name", "description", "type", "env"}, []string{"devices/all", "devices/edit"}, srv.GitHubTokenHTTPAuth, srv.editDeviceHTTPHandler, srv.GitHubTokenHTTPAuthCleanup)
	srv.http.PUT(&http.RouteDefinition{
		Host: host,
		Path: "/project/{projectid}/device/{deviceid}",
		Vars: http.Variables{
			Required: []string{"projectid", "deviceid", "tags", "name", "description", "type", "env"},
		},
		Scope: []string{"devices/all", "devices/edit"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.editDeviceHTTPHandler,
	})
	//srv.http.PUT("/project/{projectid}/devices/enable", []string{"projectid", "ids"}, []string{"devices/all", "devices/edit"}, srv.GitHubTokenHTTPAuth, srv.enableDeviceHTTPHandler, srv.GitHubTokenHTTPAuthCleanup)
	srv.http.PUT(&http.RouteDefinition{
		Host: host,
		Path: "/project/{projectid}/devices/enable",
		Vars: http.Variables{
			Required: []string{"projectid", "ids"},
		},
		Scope: []string{"devices/all", "devices/edit"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.enableDeviceHTTPHandler,
	})
	//srv.http.PUT("/project/{projectid}/devices/disable", []string{"projectid", "ids"}, []string{"devices/all", "devices/edit"}, srv.GitHubTokenHTTPAuth, srv.disableDeviceHTTPHandler, srv.GitHubTokenHTTPAuthCleanup)
	srv.http.PUT(&http.RouteDefinition{
		Host: host,
		Path: "/project/{projectid}/devices/disable",
		Vars: http.Variables{
			Required: []string{"projectid", "ids"},
		},
		Scope: []string{"devices/all", "devices/edit"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.disableDeviceHTTPHandler,
	})
	//srv.http.DELETE("/project/{projectid}/devices", []string{"projectid", "ids"}, []string{"devices/all", "devices/delete"}, srv.GitHubTokenHTTPAuth, srv.delDeviceHTTPHandler, srv.GitHubTokenHTTPAuthCleanup)
	srv.http.DELETE(&http.RouteDefinition{
		Host: host,
		Path: "/project/{projectid}/devices",
		Vars: http.Variables{
			Required: []string{"projectid", "ids"},
		},
		Scope: []string{"devices/all", "devices/delete"},
		Auth: http.RouteAuthHandler{
			Validator: srv.GitHubTokenHTTPAuth,
			GC:        srv.GitHubTokenHTTPAuthCleanup,
		},
		Handler: srv.delDeviceHTTPHandler,
	})
}
