package schema

// This file holds the per-resource index-footprint closures the IndexDriver runs
// (one per resource). Each closure declares ONLY which tns paths/entries
// its resource contributes to the compiled `indexes` subtree; the driver owns the
// append/dedup/Set mechanics and the scope walk. Kept out of definition.go so the
// DSL stays readable. The schema package may import specs + driver; the driver
// must not import the schema (one-way dependency).

import (
	"fmt"

	"github.com/taubyte/tau/core/common/repositorytype"
	"github.com/taubyte/tau/pkg/specs/common"
	databaseSpec "github.com/taubyte/tau/pkg/specs/database"
	domainSpec "github.com/taubyte/tau/pkg/specs/domain"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	librarySpec "github.com/taubyte/tau/pkg/specs/library"
	messagingSpec "github.com/taubyte/tau/pkg/specs/messaging"
	"github.com/taubyte/tau/pkg/specs/methods"
	smartOpSpec "github.com/taubyte/tau/pkg/specs/smartops"
	storageSpec "github.com/taubyte/tau/pkg/specs/storage"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/driver"
)

// domainHttpPaths is the domain fan-out shared by functions and websites: for
// each resolved domain id on the resource, look the domain up (app-then-global
// via ic.Lookup) and turn its fqdn into an http index path. httpPath differs per
// resource type only in the PathVariable it bakes in.
func domainHttpPaths(ic *driver.IndexCtx, httpPath func(string) (*common.TnsPath, error)) ([]*common.TnsPath, error) {
	domainsVal := ic.Obj.Get("domains")
	domains, ok := domainsVal.([]string)
	if !ok && domainsVal != nil {
		return nil, fmt.Errorf("domains is not a []string")
	}

	paths := make([]*common.TnsPath, 0, len(domains))
	for _, domainId := range domains {
		domObj, ok := ic.Lookup("domains", domainId)
		if !ok {
			return nil, fmt.Errorf("fetching domain object for %s failed", domainId)
		}
		fqdn, err := domObj.GetString("fqdn")
		if err != nil {
			return nil, fmt.Errorf("fqdn is not a string for domain %s: %w", domainId, err)
		}
		hp, err := httpPath(fqdn)
		if err != nil {
			return nil, fmt.Errorf("getting HTTP path for domain %s failed with %w", domainId, err)
		}
		paths = append(paths, hp)
	}
	return paths, nil
}

// repositoryPath rebuilds the git-repo reverse-index base path from a
// website/library instance's compiled provider + repository-id wire keys.
func repositoryPath(ic *driver.IndexCtx) (*methods.RepositoryPath, error) {
	provider, err := ic.Obj.GetString("provider")
	if err != nil {
		return nil, fmt.Errorf("git provider is not a string: %w", err)
	}
	repoId, err := ic.Obj.GetString("repository-id")
	if err != nil {
		return nil, fmt.Errorf("git repository is not a string: %w", err)
	}
	rp, err := methods.GetRepositoryPath(provider, repoId, ic.Project)
	if err != nil {
		return nil, fmt.Errorf("getting repository path for %s failed with %w", repoId, err)
	}
	return rp, nil
}

// functions: wasm module link + domain fan-out (both keyed off IndexValue).
func functionIndexLink(ic *driver.IndexCtx) ([]*common.TnsPath, error) {
	wasm, err := functionSpec.Tns().WasmModulePath(ic.Project, ic.App, ic.Name)
	if err != nil {
		return nil, fmt.Errorf("getting wasm module path for %s failed with %w", ic.Name, err)
	}
	domains, err := domainHttpPaths(ic, functionSpec.Tns().HttpPath)
	if err != nil {
		return nil, err
	}
	return append([]*common.TnsPath{wasm}, domains...), nil
}

// websites: domain fan-out only (no wasm) + the git-repo reverse index.
func websiteIndexLink(ic *driver.IndexCtx) ([]*common.TnsPath, error) {
	return domainHttpPaths(ic, websiteSpec.Tns().HttpPath)
}

func websiteIndexSet(ic *driver.IndexCtx) ([]driver.IndexEntry, error) {
	repoPath, err := repositoryPath(ic)
	if err != nil {
		return nil, err
	}
	return []driver.IndexEntry{
		{Path: repoPath.Type(), Value: repositorytype.WebsiteRepository},
		{Path: repoPath.Resource(ic.Id), Value: ic.IndexValue.String()},
	}, nil
}

// libraries: wasm module link + git-repo reverse index + id-keyed name index.
func libraryIndexLink(ic *driver.IndexCtx) ([]*common.TnsPath, error) {
	wasm, err := librarySpec.Tns().WasmModulePath(ic.Project, ic.App, ic.Name)
	if err != nil {
		return nil, fmt.Errorf("getting wasm module path for %s failed with %w", ic.Name, err)
	}
	return []*common.TnsPath{wasm}, nil
}

func libraryIndexSet(ic *driver.IndexCtx) ([]driver.IndexEntry, error) {
	repoPath, err := repositoryPath(ic)
	if err != nil {
		return nil, err
	}
	return []driver.IndexEntry{
		{Path: repoPath.Type(), Value: repositorytype.LibraryRepository},
		{Path: repoPath.Resource(ic.Id), Value: ic.IndexValue.String()},
		{Path: librarySpec.Tns().NameIndex(ic.Id), Value: ic.Name},
	}, nil
}

// smartops: wasm module link only.
func smartopIndexLink(ic *driver.IndexCtx) ([]*common.TnsPath, error) {
	wasm, err := smartOpSpec.Tns().WasmModulePath(ic.Project, ic.App, ic.Name)
	if err != nil {
		return nil, fmt.Errorf("getting wasm module path for %s failed with %w", ic.Name, err)
	}
	return []*common.TnsPath{wasm}, nil
}

// storages: single index-path link.
func storageIndexLink(ic *driver.IndexCtx) ([]*common.TnsPath, error) {
	return []*common.TnsPath{storageSpec.Tns().IndexPath(ic.Project, ic.App, ic.Name)}, nil
}

// databases: single index-path link.
func databaseIndexLink(ic *driver.IndexCtx) ([]*common.TnsPath, error) {
	return []*common.TnsPath{databaseSpec.Tns().IndexPath(ic.Project, ic.App, ic.Name)}, nil
}

// messaging: the per-(project,app) websocket bucket — a RAW append (no Links()
// suffix), so every messaging instance in a scope aggregates under one key.
func messagingIndexLinkRaw(ic *driver.IndexCtx) ([]*common.TnsPath, error) {
	wsp, err := messagingSpec.Tns().WebSocketHashPath(ic.Project, ic.App)
	if err != nil {
		return nil, fmt.Errorf("getting websocket hash path failed with %w", err)
	}
	return []*common.TnsPath{wsp}, nil
}

// domains: a nil placeholder at the reversed-fqdn basic path, written only when
// absent (config-compiler compat). The domain's deferred dns validation is fired
// by the driver from the fqdn attr's EmitValidation, not here.
func domainIndexSet(ic *driver.IndexCtx) ([]driver.IndexEntry, error) {
	fqdn, err := ic.Obj.GetString("fqdn")
	if err != nil {
		return nil, fmt.Errorf("domain fqdn is not a string: %w", err)
	}
	basic, err := domainSpec.Tns().BasicPath(fqdn)
	if err != nil {
		return nil, fmt.Errorf("getting basic path for domain failed with %w", err)
	}
	return []driver.IndexEntry{
		{Path: basic.Versioning().Links(), Value: nil, IfAbsent: true},
	}, nil
}
