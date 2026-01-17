package mcp

import (
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/taubyte/tau/dream"
)

type Project struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	Provider string                 `json:"provider"`
	Code     int                    `json:"code"`
	Config   int                    `json:"config"`
	Registry map[string]interface{} `json:"registry"`
	Assets   map[string]string      `json:"assets"` // resourceID -> assetPtr
}

type ListUniversesOutput struct {
	Universes []UniverseInfo `json:"universes"`
}

type UniverseInfo struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Persistent bool   `json:"persistent"`
}

type GetUniverseStatusInput struct {
	UniverseName string `json:"universe_name"`
}

type GetUniverseStatusOutput struct {
	Name       string                `json:"name"`
	Status     string                `json:"status"`
	Root       string                `json:"root"`
	SwarmKey   string                `json:"swarm_key"`
	NodeCount  int                   `json:"node_count"`
	Simples    []string              `json:"simples"`
	Nodes      map[string][]string   `json:"nodes"`
	Services   []dream.ServiceStatus `json:"services"`
	Ports      []int                 `json:"ports,omitempty"`
	DiskUsage  int64                 `json:"disk_usage,omitempty"`
	Persistent bool                  `json:"persistent"`
}

type CreateUniverseInput struct {
	UniverseName string `json:"universe_name"`
	Persistent   bool   `json:"persistent"`
}

type CreateUniverseOutput struct {
	Success      bool   `json:"success"`
	UniverseName string `json:"universe_name"`
	Message      string `json:"message"`
}

type DeleteUniverseInput struct {
	UniverseName string `json:"universe_name"`
}

type DeleteUniverseOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type StartUniverseInput struct {
	UniverseName string `json:"universe_name"`
}

type StartUniverseOutput struct {
	Success      bool   `json:"success"`
	UniverseName string `json:"universe_name"`
	Message      string `json:"message"`
}

type StopUniverseInput struct {
	UniverseName string `json:"universe_name"`
}

type StopUniverseOutput struct {
	Success      bool   `json:"success"`
	UniverseName string `json:"universe_name"`
	Message      string `json:"message"`
}

type ListProjectsInput struct {
	UniverseName string `json:"universe_name"`
}

type ListProjectsOutput struct {
	Projects []ProjectInfo      `json:"projects"`
	Schema   *jsonschema.Schema `json:"schema"`
}

type ProjectInfo struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	Provider string                 `json:"provider"`
	Code     int                    `json:"code"`
	Config   int                    `json:"config"`
	Registry map[string]interface{} `json:"registry"`
	Assets   map[string]string      `json:"assets"` // resourceID -> assetPtr
}

type GetProjectDetailsInput struct {
	UniverseName string `json:"universe_name"`
	ProjectID    string `json:"project_id"`
}

type GetProjectDetailsOutput struct {
	Project ProjectInfo        `json:"project"`
	Schema  *jsonschema.Schema `json:"schema"`
}

type GetDiskUsageInput struct {
	UniverseName string `json:"universe_name,omitempty"`
}

type GetDiskUsageOutput struct {
	UniverseName string             `json:"universe_name,omitempty"`
	DiskUsage    int64              `json:"disk_usage_bytes"`
	DiskUsageMB  int64              `json:"disk_usage_mb"`
	DiskUsageGB  int64              `json:"disk_usage_gb"`
	Schema       *jsonschema.Schema `json:"schema"`
}
