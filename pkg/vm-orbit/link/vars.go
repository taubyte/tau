package link

import (
	"errors"

	"github.com/hashicorp/go-plugin"
)

var (
	ClientPluginMap = map[string]plugin.Plugin{
		"satellite": &link{},
	}

	ErrorLinkServer = errors.New("can't create a satellite (link server) from main process")
)
