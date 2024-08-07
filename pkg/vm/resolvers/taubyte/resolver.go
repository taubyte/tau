package resolver

import (
	"errors"
	"fmt"
	"strings"

	ma "github.com/multiformats/go-multiaddr"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/specs/extract"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	librarySpec "github.com/taubyte/tau/pkg/specs/library"
	"github.com/taubyte/tau/pkg/specs/methods"
	smartOpSpec "github.com/taubyte/tau/pkg/specs/smartops"
)

type resolver struct {
	tns tns.Client
}

var _ vm.Resolver = &resolver{}

func New(client tns.Client) vm.Resolver {
	return &resolver{
		tns: client,
	}
}

func (s *resolver) Lookup(ctx vm.Context, name string) (ma.Multiaddr, error) {
	splitAddress := strings.Split(name, "/")
	splitAddressLen := len(splitAddress)
	if splitAddressLen < 2 {
		return nil, fmt.Errorf("invalid module name format `%s`", name)
	}

	moduleType := splitAddress[0]
	if moduleType == "" {
		multiAddr, err := ma.NewMultiaddr(name)
		if err != nil {
			return nil, fmt.Errorf("parsing multi-address `%s` failed with: %s", name, err)
		}

		return multiAddr, nil
	} else if s.tns != nil {
		switch moduleType {
		case functionSpec.PathVariable.String(), smartOpSpec.PathVariable.String(), librarySpec.PathVariable.String(): // supported module types
			if splitAddressLen != 2 {
				return nil, fmt.Errorf(
					"invalid local module name got `%s` expected <%s|%s|%s>/<name>",
					name,
					functionSpec.PathVariable.String(),
					smartOpSpec.PathVariable.String(),
					librarySpec.PathVariable.String(),
				)
			}
			return projectRelativeToCid(ctx, s.tns, moduleType, splitAddress[1])
		default:
			return nil, fmt.Errorf("invalid moduleType `%s`", moduleType)
		}
	} else {
		return nil, fmt.Errorf("no TNS found")
	}
}

func projectRelativeToCid(ctx vm.Context, tns tns.Client, moduleType string, moduleName string) (ma.Multiaddr, error) {
	project := ctx.Project()
	application := ctx.Application()
	branches := ctx.Branches()

	// Get current commit index with function context Application
	currentPaths, err := currentWasmModule(tns, moduleType, moduleName, project, application, branches...)
	if err != nil {
		if len(application) < 1 {
			return nil, err
		}

		// If no current commit index found, with a non empty application try global
		currentPaths, err = currentWasmModule(tns, moduleType, moduleName, project, "", branches...)
		if err != nil {
			return nil, fmt.Errorf("looking up global and local modules failed with: %s", err)
		}
	}

	assetCid, err := fetchCidFromCurrent(currentPaths, tns, project)
	if err != nil {
		return nil, fmt.Errorf("fetching cid for module `%s/%s` on project `%s` with application `%s` failed with: %s", moduleType, moduleName, project, application, err)
	}

	return ma.NewMultiaddr("/dfs/" + assetCid)
}

func currentWasmModule(tns tns.Client, moduleType, moduleName, project, application string, branches ...string) ([]tns.Path, error) {
	wasmModulePath, err := methods.WasmModulePathFromModule(project, application, moduleType, moduleName)
	if err != nil {
		return nil, err
	}

	wasmIndex, err := tns.Fetch(wasmModulePath)
	if err != nil {
		return nil, fmt.Errorf("looking up module `%s/%s` with app: `%s` in project `%s` failed with: %s", moduleType, moduleName, application, project, err)
	}

	currentPath, err := wasmIndex.Current(branches)
	if err != nil {
		return nil, fmt.Errorf("looking up current commit for module `%s/%s` with app: `%s` in project `%s` failed with: %s", moduleType, moduleName, application, project, err)
	}

	return currentPath, nil
}

func fetchCidFromCurrent(currentPaths []tns.Path, tns tns.Client, project string) (string, error) {
	// Current is expected to index one value in the slice
	if len(currentPaths) > 1 {
		return "", errors.New("current returned too many paths, theres an issue with the config compiler")
	}

	parser, err := extract.Tns().BasicPath(currentPaths[0].String())
	if err != nil {
		return "", err
	}

	assetHash, err := methods.GetTNSAssetPath(project, parser.Resource(), parser.Branch())
	if err != nil {
		return "", fmt.Errorf("getting asset hash failed with: %s", err)
	}

	_assetCid, err := tns.Fetch(assetHash)
	if err != nil {
		return "", fmt.Errorf("fetching asset from hash `%s` failed with: %s", assetHash, err)
	}

	assetCid, ok := _assetCid.Interface().(string)
	if !ok {
		return "", fmt.Errorf("asset type `%T` unexpected", _assetCid.Interface())
	}

	return assetCid, nil
}
