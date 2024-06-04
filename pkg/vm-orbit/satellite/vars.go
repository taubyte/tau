package satellite

import (
	"errors"
	"reflect"

	"github.com/hashicorp/go-plugin"
)

var (
	ServerPluginMap = map[string]plugin.Plugin{
		"satellite": &satellite{},
	}

	moduleType = reflect.TypeOf((*Module)(nil)).Elem()

	ErrorLinkClient = errors.New("can't create a link (satellite client) from main process")
)
