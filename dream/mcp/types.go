package mcp

import "github.com/taubyte/tau/dream"

// Project represents a project in a universe
type Project struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	Provider string                 `json:"provider"`
	Code     int                    `json:"code"`
	Config   int                    `json:"config"`
	Registry map[string]interface{} `json:"registry"`
	Assets   map[string]string      `json:"assets"` // resourceID -> assetPtr
}

// Universe management types

type ListUniversesOutput struct {
	Universes []UniverseInfo `json:"universes"`
	Schema    interface{}    `json:"schema,omitempty"`
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
	Schema     interface{}           `json:"schema,omitempty"`
}

type CreateUniverseInput struct {
	UniverseName string `json:"universe_name"`
	Persistent   bool   `json:"persistent"`
}

type CreateUniverseOutput struct {
	Success      bool        `json:"success"`
	UniverseName string      `json:"universe_name"`
	Message      string      `json:"message"`
	Schema       interface{} `json:"schema,omitempty"`
}

type DeleteUniverseInput struct {
	UniverseName string `json:"universe_name"`
}

type DeleteUniverseOutput struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Schema  interface{} `json:"schema,omitempty"`
}

type StartUniverseInput struct {
	UniverseName string `json:"universe_name"`
}

type StartUniverseOutput struct {
	Success      bool        `json:"success"`
	UniverseName string      `json:"universe_name"`
	Message      string      `json:"message"`
	Schema       interface{} `json:"schema,omitempty"`
}

type StopUniverseInput struct {
	UniverseName string `json:"universe_name"`
}

type StopUniverseOutput struct {
	Success      bool        `json:"success"`
	UniverseName string      `json:"universe_name"`
	Message      string      `json:"message"`
	Schema       interface{} `json:"schema,omitempty"`
}

// Project management types

type ListProjectsInput struct {
	UniverseName string `json:"universe_name"`
}

type ListProjectsOutput struct {
	Projects []ProjectInfo `json:"projects"`
	Schema   interface{}   `json:"schema,omitempty"`
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
	Project ProjectInfo `json:"project"`
	Schema  interface{} `json:"schema,omitempty"`
}

// Resource management types

type GetDiskUsageInput struct {
	UniverseName string `json:"universe_name,omitempty"`
}

type GetDiskUsageOutput struct {
	UniverseName string      `json:"universe_name,omitempty"`
	DiskUsage    int64       `json:"disk_usage_bytes"`
	DiskUsageMB  int64       `json:"disk_usage_mb"`
	DiskUsageGB  int64       `json:"disk_usage_gb"`
	Schema       interface{} `json:"schema,omitempty"`
}

type DownloadAssetInput struct {
	UniverseName string `json:"universe_name"`
	CID          string `json:"cid"`
	AssetName    string `json:"asset_name"`
}

type DownloadAssetOutput struct {
	Success  bool        `json:"success"`
	FilePath string      `json:"file_path,omitempty"`
	Message  string      `json:"message"`
	Schema   interface{} `json:"schema,omitempty"`
}

// System monitoring types

type GetSystemMetricsOutput struct {
	CPUUsagePercent float64     `json:"cpu_usage_percent"`
	MemoryUsage     int64       `json:"memory_usage_bytes"`
	MemoryUsageMB   int64       `json:"memory_usage_mb"`
	ProcessID       int         `json:"process_id"`
	Uptime          string      `json:"uptime"`
	Schema          interface{} `json:"schema,omitempty"`
}

type GetDNSStatusOutput struct {
	Status  string      `json:"status"`
	Message string      `json:"message"`
	Schema  interface{} `json:"schema,omitempty"`
}

// Logging types

type GetLogsInput struct {
	UniverseName string `json:"universe_name,omitempty"`
	ServiceName  string `json:"service_name,omitempty"`
	Lines        int    `json:"lines,omitempty"`
}

type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Service   string `json:"service,omitempty"`
}

type GetLogsOutput struct {
	Logs   []LogEntry  `json:"logs"`
	Schema interface{} `json:"schema,omitempty"`
}
