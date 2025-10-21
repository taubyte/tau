package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	tnsCore "github.com/taubyte/tau/core/services/tns"
	specsCommon "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
	"github.com/taubyte/tau/utils/maps"
)

func convertRegistryToMapAndIDs(registry map[interface{}]interface{}) (map[string]interface{}, []string, error) {
	result := make(map[string]interface{})
	var ids []string

	for key, value := range registry {
		keyStr, ok := key.(string)
		if !ok {
			return nil, nil, fmt.Errorf("registry key is not a string: %v", key)
		}

		if keyStr == "id" {
			if idValue, ok := value.(string); ok {
				ids = append(ids, idValue)
			} else {
				ids = append(ids, fmt.Sprintf("%v", value))
			}
		} else if len(keyStr) >= 2 && keyStr[:2] == "Qm" {
			ids = append(ids, keyStr)
		}

		switch v := value.(type) {
		case map[interface{}]interface{}:
			nested, nestedIDs, err := convertRegistryToMapAndIDs(v)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to convert nested map for key %s: %w", keyStr, err)
			}
			result[keyStr] = nested
			ids = append(ids, nestedIDs...)
		case map[string]interface{}:
			for nestedKey, nestedValue := range v {
				if nestedKey == "id" {
					if idValue, ok := nestedValue.(string); ok {
						ids = append(ids, idValue)
					} else {
						ids = append(ids, fmt.Sprintf("%v", nestedValue))
					}
				} else if len(nestedKey) >= 2 && nestedKey[:2] == "Qm" {
					ids = append(ids, nestedKey)
				}
			}
			result[keyStr] = v
		case []interface{}:
			convertedSlice := make([]interface{}, len(v))
			for i, item := range v {
				if itemMap, ok := item.(map[interface{}]interface{}); ok {
					convertedItem, itemIDs, err := convertRegistryToMapAndIDs(itemMap)
					if err != nil {
						return nil, nil, fmt.Errorf("failed to convert slice item %d for key %s: %w", i, keyStr, err)
					}
					convertedSlice[i] = convertedItem
					ids = append(ids, itemIDs...)
				} else {
					convertedSlice[i] = item
				}
			}
			result[keyStr] = convertedSlice
		case string, int, int64, float64, bool, nil:
			result[keyStr] = v
		default:
			result[keyStr] = fmt.Sprintf("%v", v)
		}
	}

	return result, ids, nil
}

func (m *Service) listProjects(ctx context.Context, req *mcp.CallToolRequest, input ListProjectsInput) (*mcp.CallToolResult, ListProjectsOutput, error) {
	u, err := m.multiverse.Universe(input.UniverseName)
	if err != nil {
		return errorListProjects("universe %s not found: %v", input.UniverseName, err)
	}

	if u.Auth() == nil || u.Auth().KV() == nil {
		return errorListProjects("auth service not available for universe %s", input.UniverseName)
	}

	client, err := u.Simple("client")
	if err != nil {
		return errorListProjects("failed to get client node for universe %s: %v", input.UniverseName, err)
	}

	auth, err := client.Auth()
	if err != nil {
		return errorListProjects("failed to get auth client for universe %s: %v", input.UniverseName, err)
	}

	tns, err := client.TNS()
	if err != nil {
		return errorListProjects("failed to get tns client for universe %s: %v", input.UniverseName, err)
	}

	assets := make(map[string]string)
	assetsKeys, err := tns.Lookup(tnsCore.Query{Prefix: []string{"assets"}})
	if err != nil {
		return errorListProjects("failed to get assets for universe %s: %v", input.UniverseName, err)
	}

	switch assetsKeys := assetsKeys.(type) {
	case []string:
		for _, asset := range assetsKeys {
			path := strings.Split(asset, "/")
			if len(path) < 3 {
				continue
			}
			assetPtr := path[2]
			ao, err := tns.Fetch(specsCommon.NewTnsPath(path[1:]))
			if err != nil {
				return errorListProjects("failed to get asset for universe %s: %v", input.UniverseName, err)
			}
			switch ao.Interface().(type) {
			case string:
				assets[assetPtr] = ao.Interface().(string)
			}
		}
	}

	projects, err := auth.Projects().List()
	if err != nil {
		return errorListProjects("failed to list projects for universe %s: %v", input.UniverseName, err)
	}

	projectInfos := make([]ProjectInfo, 0, len(projects))
	for _, projectID := range projects {
		projectStruct := auth.Projects().Get(projectID)

		var (
			registryMap map[string]interface{}
			ids         []string
			assetsMap   map[string]string
		)

		registry, _ := tns.Simple().Project(projectID, specsCommon.DefaultBranches...)
		if registry != nil {
			registryMapAny, ok := registry.(map[interface{}]interface{})
			if ok {
				registryMap, ids, err = convertRegistryToMapAndIDs(registryMapAny)
				if err != nil {
					registryMap = make(map[string]interface{})
					ids = []string{}
				}
			}
		}

		assetsMap = make(map[string]string)
		for _, id := range ids {
			for _, branch := range specsCommon.DefaultBranches {
				assetPath, err := methods.GetTNSAssetPath(projectID, id, branch)
				if err == nil {
					assetPtr := assetPath.Slice()[1]
					if _, ok := assets[assetPtr]; ok {
						assetsMap[assetPtr] = id
					}
				}
			}
		}

		projectInfos = append(projectInfos, ProjectInfo{
			ID:       projectID,
			Name:     projectStruct.Name,
			Provider: projectStruct.Provider,
			Code:     projectStruct.Git.Code.Id(),
			Config:   projectStruct.Git.Config.Id(),
			Registry: registryMap,
			Assets:   assetsMap,
		})
	}

	return nil, ListProjectsOutput{
		Projects: projectInfos,
		Schema:   ListProjectsOutputSchema,
	}, nil
}

func (m *Service) getProjectDetails(ctx context.Context, req *mcp.CallToolRequest, input GetProjectDetailsInput) (*mcp.CallToolResult, GetProjectDetailsOutput, error) {
	u, err := m.multiverse.Universe(input.UniverseName)
	if err != nil {
		return errorGetProjectDetails("universe %s not found: %v", input.UniverseName, err)
	}

	if u.Auth() == nil || u.Auth().KV() == nil {
		return errorGetProjectDetails("auth service not available for universe %s", input.UniverseName)
	}

	client, err := u.Simple("client")
	if err != nil {
		return errorGetProjectDetails("failed to get client node for universe %s: %v", input.UniverseName, err)
	}

	auth, err := client.Auth()
	if err != nil {
		return errorGetProjectDetails("failed to get auth client for universe %s: %v", input.UniverseName, err)
	}

	tns, err := client.TNS()
	if err != nil {
		return errorGetProjectDetails("failed to get tns client for universe %s: %v", input.UniverseName, err)
	}

	projects, err := auth.Projects().List()
	if err != nil {
		return errorGetProjectDetails("failed to list projects for universe %s: %v", input.UniverseName, err)
	}

	var projectExists bool
	for _, projectID := range projects {
		if projectID == input.ProjectID {
			projectExists = true
			break
		}
	}

	if !projectExists {
		return errorGetProjectDetails("project %s not found in universe %s", input.ProjectID, input.UniverseName)
	}

	projectStruct := auth.Projects().Get(input.ProjectID)

	assetsObject, err := tns.Fetch(specsCommon.NewTnsPath([]string{"assets"}))
	if err != nil {
		return errorGetProjectDetails("failed to get assets for universe %s: %v", input.UniverseName, err)
	}

	assets := maps.SafeInterfaceToString[string](assetsObject.Interface())

	var (
		registryMap map[string]interface{}
		ids         []string
		assetsMap   map[string]string
	)

	registry, _ := tns.Simple().Project(input.ProjectID, specsCommon.DefaultBranches...)
	if registry != nil {
		registryMapAny, ok := registry.(map[interface{}]interface{})
		if ok {
			registryMap, ids, err = convertRegistryToMapAndIDs(registryMapAny)
			if err != nil {
				registryMap = make(map[string]interface{})
				ids = []string{}
			}
		}
	}

	assetsMap = make(map[string]string)
	for _, id := range ids {
		for _, branch := range specsCommon.DefaultBranches {
			assetPath, err := methods.GetTNSAssetPath(input.ProjectID, id, branch)
			if err == nil {
				assetPtr := assetPath.Slice()[1]
				if _, ok := assets[assetPtr]; ok {
					assetsMap[assetPtr] = id
				}
			}
		}
	}

	projectInfo := ProjectInfo{
		ID:       input.ProjectID,
		Name:     projectStruct.Name,
		Provider: projectStruct.Provider,
		Code:     projectStruct.Git.Code.Id(),
		Config:   projectStruct.Git.Config.Id(),
		Registry: registryMap,
		Assets:   assetsMap,
	}

	return nil, GetProjectDetailsOutput{
		Project: projectInfo,
		Schema:  GetProjectDetailsOutputSchema,
	}, nil
}
