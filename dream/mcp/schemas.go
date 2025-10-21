package mcp

import "github.com/google/jsonschema-go/jsonschema"

var (
	ListUniversesInputSchema = &jsonschema.Schema{
		Type:                 "object",
		Properties:           map[string]*jsonschema.Schema{},
		AdditionalProperties: &jsonschema.Schema{Type: "boolean", Const: &[]any{false}[0]},
	}

	GetUniverseStatusInputSchema = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"universe_name": {
				Type:        "string",
				Description: "Name of the universe to get status for",
			},
		},
		Required:             []string{"universe_name"},
		AdditionalProperties: &jsonschema.Schema{Type: "boolean", Const: &[]any{false}[0]},
	}

	CreateUniverseInputSchema = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"universe_name": {
				Type:        "string",
				Description: "Name for the new universe",
			},
			"persistent": {
				Type:        "boolean",
				Description: "Whether the universe should be persistent",
			},
		},
		Required:             []string{"universe_name", "persistent"},
		AdditionalProperties: &jsonschema.Schema{Type: "boolean", Const: &[]any{false}[0]},
	}

	DeleteUniverseInputSchema = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"universe_name": {
				Type:        "string",
				Description: "Name of the universe to delete",
			},
		},
		Required:             []string{"universe_name"},
		AdditionalProperties: &jsonschema.Schema{Type: "boolean", Const: &[]any{false}[0]},
	}

	StartUniverseInputSchema = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"universe_name": {
				Type:        "string",
				Description: "Name of the universe to start",
			},
		},
		Required:             []string{"universe_name"},
		AdditionalProperties: &jsonschema.Schema{Type: "boolean", Const: &[]any{false}[0]},
	}

	StopUniverseInputSchema = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"universe_name": {
				Type:        "string",
				Description: "Name of the universe to stop",
			},
		},
		Required:             []string{"universe_name"},
		AdditionalProperties: &jsonschema.Schema{Type: "boolean", Const: &[]any{false}[0]},
	}

	ListProjectsInputSchema = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"universe_name": {
				Type:        "string",
				Description: "Name of the universe to list projects from",
			},
		},
		Required:             []string{"universe_name"},
		AdditionalProperties: &jsonschema.Schema{Type: "boolean", Const: &[]any{false}[0]},
	}

	GetProjectDetailsInputSchema = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"universe_name": {
				Type:        "string",
				Description: "Name of the universe containing the project",
			},
			"project_id": {
				Type:        "string",
				Description: "ID of the project to get details for",
			},
		},
		Required:             []string{"universe_name", "project_id"},
		AdditionalProperties: &jsonschema.Schema{Type: "boolean", Const: &[]any{false}[0]},
	}

	GetDiskUsageInputSchema = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"universe_name": {
				Type:        "string",
				Description: "Name of the universe to get disk usage for (optional)",
			},
		},
		AdditionalProperties: &jsonschema.Schema{Type: "boolean", Const: &[]any{false}[0]},
	}
)

var (
	ListUniversesOutputSchema = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"universes": {
				Type: "array",
				Items: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"name": {
							Type:        "string",
							Description: "Unique name identifier for the universe",
						},
						"status": {
							Type:        "string",
							Description: "Current status of the universe",
							Enum:        []any{"started", "stopped"},
						},
						"persistent": {
							Type:        "boolean",
							Description: "Whether the universe is persistent",
						},
					},
					Required: []string{"name", "status", "persistent"},
				},
				Description: "List of universes in the multiverse",
			},
		},
		Required: []string{"universes"},
	}
	GetUniverseStatusOutputSchema = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"name": {
				Type:        "string",
				Description: "Unique name identifier for the universe",
			},
			"status": {
				Type:        "string",
				Description: "Current status of the universe",
				Enum:        []any{"started", "stopped"},
			},
			"root": {
				Type:        "string",
				Description: "Root directory path for the universe",
			},
			"swarm_key": {
				Type:        "string",
				Description: "Swarm key for the universe",
			},
			"node_count": {
				Type:        "integer",
				Description: "Number of nodes in the universe",
			},
			"simples": {
				Type:        "array",
				Items:       &jsonschema.Schema{Type: "string"},
				Description: "List of simple nodes in the universe",
			},
			"nodes": {
				Type:        "object",
				Description: "Map of node IDs to their addresses",
				AdditionalProperties: &jsonschema.Schema{
					Type:        "array",
					Items:       &jsonschema.Schema{Type: "string"},
					Description: "List of addresses for the node",
				},
			},
			"services": {
				Type:        "array",
				Items:       &jsonschema.Schema{Type: "object"},
				Description: "List of services running in the universe",
			},
			"ports": {
				Type:        "array",
				Items:       &jsonschema.Schema{Type: "integer"},
				Description: "List of ports used by the universe",
			},
			"disk_usage": {
				Type:        "integer",
				Description: "Disk usage in bytes",
			},
			"persistent": {
				Type:        "boolean",
				Description: "Whether the universe is persistent",
			},
		},
		Required: []string{"name", "status", "root", "swarm_key", "node_count", "simples", "nodes", "services", "persistent"},
	}
	CreateUniverseOutputSchema = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"success": {
				Type:        "boolean",
				Description: "Whether the universe was created successfully",
			},
			"universe_name": {
				Type:        "string",
				Description: "Name of the created universe",
			},
			"message": {
				Type:        "string",
				Description: "Status message",
			},
		},
		Required: []string{"success", "universe_name", "message"},
	}

	DeleteUniverseOutputSchema = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"success": {
				Type:        "boolean",
				Description: "Whether the universe was deleted successfully",
			},
			"message": {
				Type:        "string",
				Description: "Status message",
			},
		},
		Required: []string{"success", "message"},
	}

	StartUniverseOutputSchema = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"success": {
				Type:        "boolean",
				Description: "Whether the universe was started successfully",
			},
			"universe_name": {
				Type:        "string",
				Description: "Name of the started universe",
			},
			"message": {
				Type:        "string",
				Description: "Status message",
			},
		},
		Required: []string{"success", "universe_name", "message"},
	}

	StopUniverseOutputSchema = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"success": {
				Type:        "boolean",
				Description: "Whether the universe was stopped successfully",
			},
			"universe_name": {
				Type:        "string",
				Description: "Name of the stopped universe",
			},
			"message": {
				Type:        "string",
				Description: "Status message",
			},
		},
		Required: []string{"success", "universe_name", "message"},
	}
	ListProjectsOutputSchema = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"projects": {
				Type: "array",
				Items: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"id": {
							Type:        "string",
							Description: "Unique project identifier within the universe",
						},
						"name": {
							Type:        "string",
							Description: "Human-readable project name",
						},
						"provider": {
							Type:        "string",
							Description: "Project provider (e.g., 'github', 'gitlab', 'local')",
						},
						"code": {
							Type:        "integer",
							Description: "Code repository ID - references the code repository in the project's git configuration",
						},
						"config": {
							Type:        "integer",
							Description: "Configuration repository ID - references the configuration repository in the project's git configuration",
						},
						"registry": {
							Type:        "object",
							Description: "Project registry containing resource definitions and configurations",
							AdditionalProperties: &jsonschema.Schema{
								Type:        "object",
								Description: "Resource configuration object",
							},
						},
						"assets": {
							Type:        "object",
							Description: "Project assets mapping resource IDs to asset pointers",
							AdditionalProperties: &jsonschema.Schema{
								Type:        "string",
								Description: "Asset pointer (CID) for the resource",
							},
						},
					},
					Required: []string{"id", "name", "provider", "code", "config", "registry", "assets"},
				},
				Description: "List of projects in the universe with full details",
			},
		},
		Required: []string{"projects"},
	}
	GetProjectDetailsOutputSchema = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"project": {
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"id": {
						Type:        "string",
						Description: "Unique project identifier within the universe",
					},
					"name": {
						Type:        "string",
						Description: "Human-readable project name",
					},
					"provider": {
						Type:        "string",
						Description: "Project provider (e.g., 'github', 'gitlab', 'local')",
					},
					"code": {
						Type:        "integer",
						Description: "Code repository ID - references the code repository in the project's git configuration",
					},
					"config": {
						Type:        "integer",
						Description: "Configuration repository ID - references the configuration repository in the project's git configuration",
					},
					"registry": {
						Type:        "object",
						Description: "Project registry containing resource definitions and configurations",
						AdditionalProperties: &jsonschema.Schema{
							Type:        "object",
							Description: "Resource configuration object",
						},
					},
					"assets": {
						Type:        "object",
						Description: "Project assets mapping resource IDs to asset pointers",
						AdditionalProperties: &jsonschema.Schema{
							Type:        "string",
							Description: "Asset pointer (CID) for the resource",
						},
					},
				},
				Required: []string{"id", "name", "provider", "code", "config", "registry", "assets"},
			},
		},
		Required: []string{"project"},
	}

	GetDiskUsageOutputSchema = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"universe_name": {
				Type:        "string",
				Description: "Name of the universe (if querying specific universe)",
			},
			"disk_usage_bytes": {
				Type:        "integer",
				Description: "Disk usage in bytes",
			},
			"disk_usage_mb": {
				Type:        "integer",
				Description: "Disk usage in megabytes",
			},
			"disk_usage_gb": {
				Type:        "integer",
				Description: "Disk usage in gigabytes",
			},
		},
		Required: []string{"disk_usage_bytes", "disk_usage_mb", "disk_usage_gb"},
	}
)
