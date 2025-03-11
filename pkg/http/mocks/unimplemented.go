package mocks

import (
	"context"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"
	service "github.com/taubyte/tau/pkg/http"
)

func NewUnimplemented(ctx context.Context) service.Service {
	return &unimplementedHttp{}
}

type unimplementedHttp struct {
	ctx context.Context
}

func (u *unimplementedHttp) Context() context.Context                      { return u.ctx }
func (*unimplementedHttp) Start()                                          {}
func (*unimplementedHttp) Stop()                                           {}
func (*unimplementedHttp) Wait() error                                     { return nil }
func (*unimplementedHttp) Error() error                                    { return nil }
func (*unimplementedHttp) GET(*service.RouteDefinition)                    {}
func (*unimplementedHttp) PUT(*service.RouteDefinition)                    {}
func (*unimplementedHttp) POST(*service.RouteDefinition)                   {}
func (*unimplementedHttp) DELETE(*service.RouteDefinition)                 {}
func (*unimplementedHttp) PATCH(*service.RouteDefinition)                  {}
func (*unimplementedHttp) ALL(*service.RouteDefinition)                    {}
func (*unimplementedHttp) Raw(*service.RawRouteDefinition) *mux.Route      { return nil }
func (*unimplementedHttp) LowLevel(*service.LowLevelDefinition) *mux.Route { return nil }
func (*unimplementedHttp) WebSocket(*service.WebSocketDefinition)          {}
func (*unimplementedHttp) ServeAssets(*service.AssetsDefinition)           {}
func (*unimplementedHttp) GetListenAddress() (*url.URL, error)             { return nil, nil }
func (*unimplementedHttp) AssetHandler(*service.HeadlessAssetsDefinition, service.Context) (interface{}, error) {
	return nil, nil
}
func (*unimplementedHttp) LowLevelAssetHandler(*service.HeadlessAssetsDefinition, http.ResponseWriter, *http.Request) error {
	return nil
}
