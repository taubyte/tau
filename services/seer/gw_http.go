package seer

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/h2non/filetype"
	"github.com/h2non/filetype/matchers"
	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/config-compiler/decompile"
	http "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/pkg/schema/pretty"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"
	commonSpec "github.com/taubyte/tau/pkg/specs/common"

	"github.com/taubyte/utils/maps"
)

//FIXME: this is temporary here, should move to gw node (we will create at some point morty)

var _ pretty.Prettier = &safeEngine{}

type safeEngine struct {
	srv      *Service
	project  string
	branches []string
}

func (e *safeEngine) Fetch(path *commonSpec.TnsPath) (pretty.Object, error) {
	return e.srv.tns.Fetch(path)
}

func (e *safeEngine) Project() string {
	return e.project
}

func (e *safeEngine) Branches() []string {
	return e.branches
}

func (srv *Service) getProjectFromContext(ctx http.Context) (projectSchema.Project, error) {
	projectId, err := maps.String(ctx.Variables(), "projectId")
	if err != nil {
		return nil, err
	}

	projectObj, err := srv.tns.Simple().Project(projectId, commonSpec.DefaultBranches...)
	if err != nil {
		return nil, fmt.Errorf("fetching project object failed: %w, %s", err, "Retry this job")
	}

	decompiler, err := decompile.New(afero.NewMemMapFs(), projectObj)
	if err != nil {
		return nil, err
	}

	projectIface, err := decompiler.Build()
	if err != nil {
		return nil, fmt.Errorf("decompiling project failed with: %w", err)
	}

	return projectIface, nil
}

func (srv *Service) projectConfigHandler(ctx http.Context) (interface{}, error) {
	projectIface, err := srv.getProjectFromContext(ctx)
	if err != nil {
		return nil, err
	}

	engine := &safeEngine{
		srv:      srv,
		project:  projectIface.Get().Id(),
		branches: commonSpec.DefaultBranches,
	}
	prettyObj := projectIface.Prettify(engine)

	return prettyObj, nil
}

func (srv *Service) downloadAsset(ctx http.Context) (interface{}, error) {
	// Get project id

	// TODO: use the projectId to confirm a user has access to the asset
	// projectId, err := maps.String(ctx.Variables, "projectId")
	// if err != nil {
	// 	return nil, err
	// }

	// Get asset id
	assetCID, err := maps.String(ctx.Variables(), "assetCID")
	if err != nil {
		return nil, err
	}

	_ctx, _ctxC := context.WithTimeout(srv.node.Context(), 60*time.Second)
	defer _ctxC()

	file, err := srv.node.GetFile(_ctx, assetCID)
	if err != nil {
		return nil, fmt.Errorf("failed grabbing asset cid %s with %v", assetCID, err)
	}

	typeBuff := make([]byte, 512)

	file.Read(typeBuff)
	defer file.Close()

	file.Seek(0, 0)
	fileData, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed reading asset file %s with %v", assetCID, err)
	}

	contentType, err := filetype.Match(typeBuff)
	if err != nil {
		return nil, fmt.Errorf("failed filetype match for asset %s wtih %v", assetCID, err)
	}

	if contentType == matchers.TypeZip {
		return http.RawData{ContentType: "application/zip", Data: fileData}, nil
	} else {
		return http.RawData{ContentType: "application/wasm", Data: fileData}, nil
	}
}

func (srv *Service) setupTNSGatewayHTTPRoutes() {
	var host string
	if !srv.devMode && len(srv.hostUrl) > 0 {
		host = "seer.tau." + srv.hostUrl
	}

	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/config/{projectId}",
		Vars: http.Variables{
			Required: []string{"projectId"},
		},
		Handler: srv.projectConfigHandler,
	})

	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/download/{projectId}/{assetCID}",
		Vars: http.Variables{
			Required: []string{"projectId", "assetCID"},
		},
		Handler:     srv.downloadAsset,
		RawResponse: true,
	})
}
