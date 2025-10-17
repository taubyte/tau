package mcp

// getSchemaForType returns the JSON schema for a given data type
func (m *MCPService) getSchemaForType(dataType string) interface{} {
	switch dataType {
	case "list_universes_output":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"universes": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"name": map[string]interface{}{
								"type":        "string",
								"description": "Unique name identifier for the universe",
							},
							"status": map[string]interface{}{
								"type":        "string",
								"description": "Current status of the universe",
								"enum":        []string{"started", "stopped"},
							},
							"persistent": map[string]interface{}{
								"type":        "boolean",
								"description": "Whether the universe is persistent",
							},
						},
						"required": []string{"name", "status", "persistent"},
					},
					"description": "List of universes in the multiverse",
				},
			},
			"required": []string{"universes"},
		}
	case "get_universe_status_output":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Unique name identifier for the universe",
				},
				"status": map[string]interface{}{
					"type":        "string",
					"description": "Current status of the universe",
					"enum":        []string{"started", "stopped"},
				},
				"root": map[string]interface{}{
					"type":        "string",
					"description": "Root directory path for the universe",
				},
				"swarm_key": map[string]interface{}{
					"type":        "string",
					"description": "Swarm key for the universe",
				},
				"node_count": map[string]interface{}{
					"type":        "integer",
					"description": "Number of nodes in the universe",
				},
				"simples": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "List of simple nodes in the universe",
				},
				"nodes": map[string]interface{}{
					"type":        "object",
					"description": "Map of node IDs to their addresses",
					"additionalProperties": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "List of addresses for the node",
					},
				},
				"services": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "object"},
					"description": "List of services running in the universe",
				},
				"ports": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "integer"},
					"description": "List of ports used by the universe",
				},
				"disk_usage": map[string]interface{}{
					"type":        "integer",
					"description": "Disk usage in bytes",
				},
				"persistent": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether the universe is persistent",
				},
			},
			"required": []string{"name", "status", "root", "swarm_key", "node_count", "simples", "nodes", "services", "persistent"},
		}
	case "create_universe_output":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"success": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether the universe was created successfully",
				},
				"universe_name": map[string]interface{}{
					"type":        "string",
					"description": "Name of the created universe",
				},
				"message": map[string]interface{}{
					"type":        "string",
					"description": "Status message",
				},
			},
			"required": []string{"success", "universe_name", "message"},
		}
	case "delete_universe_output":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"success": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether the universe was deleted successfully",
				},
				"message": map[string]interface{}{
					"type":        "string",
					"description": "Status message",
				},
			},
			"required": []string{"success", "message"},
		}
	case "start_universe_output":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"success": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether the universe was started successfully",
				},
				"universe_name": map[string]interface{}{
					"type":        "string",
					"description": "Name of the started universe",
				},
				"message": map[string]interface{}{
					"type":        "string",
					"description": "Status message",
				},
			},
			"required": []string{"success", "universe_name", "message"},
		}
	case "stop_universe_output":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"success": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether the universe was stopped successfully",
				},
				"universe_name": map[string]interface{}{
					"type":        "string",
					"description": "Name of the stopped universe",
				},
				"message": map[string]interface{}{
					"type":        "string",
					"description": "Status message",
				},
			},
			"required": []string{"success", "universe_name", "message"},
		}
	case "list_projects_output":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"projects": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"id": map[string]interface{}{
								"type":        "string",
								"description": "Unique project identifier within the universe",
							},
							"name": map[string]interface{}{
								"type":        "string",
								"description": "Human-readable project name",
							},
							"provider": map[string]interface{}{
								"type":        "string",
								"description": "Project provider (e.g., 'github', 'gitlab', 'local')",
							},
							"code": map[string]interface{}{
								"type":        "integer",
								"description": "Code repository ID - references the code repository in the project's git configuration",
							},
							"config": map[string]interface{}{
								"type":        "integer",
								"description": "Configuration repository ID - references the configuration repository in the project's git configuration",
							},
							"registry": map[string]interface{}{
								"type":        "object",
								"description": "Project registry containing resource definitions and configurations",
								"additionalProperties": map[string]interface{}{
									"type":        "object",
									"description": "Resource configuration object",
								},
							},
							"assets": map[string]interface{}{
								"type":        "object",
								"description": "Project assets mapping resource IDs to asset pointers",
								"additionalProperties": map[string]interface{}{
									"type":        "string",
									"description": "Asset pointer (CID) for the resource",
								},
							},
						},
						"required": []string{"id", "name", "provider", "code", "config", "registry", "assets"},
					},
					"description": "List of projects in the universe",
				},
			},
			"required": []string{"projects"},
		}
	case "get_project_details_output":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{
							"type":        "string",
							"description": "Unique project identifier within the universe",
						},
						"name": map[string]interface{}{
							"type":        "string",
							"description": "Human-readable project name",
						},
						"provider": map[string]interface{}{
							"type":        "string",
							"description": "Project provider (e.g., 'github', 'gitlab', 'local')",
						},
						"code": map[string]interface{}{
							"type":        "integer",
							"description": "Code repository ID - references the code repository in the project's git configuration",
						},
						"config": map[string]interface{}{
							"type":        "integer",
							"description": "Configuration repository ID - references the configuration repository in the project's git configuration",
						},
						"registry": map[string]interface{}{
							"type":        "object",
							"description": "Project registry containing resource definitions and configurations",
							"additionalProperties": map[string]interface{}{
								"type":        "object",
								"description": "Resource configuration object",
							},
						},
						"assets": map[string]interface{}{
							"type":        "object",
							"description": "Project assets mapping resource IDs to asset pointers",
							"additionalProperties": map[string]interface{}{
								"type":        "string",
								"description": "Asset pointer (CID) for the resource",
							},
						},
					},
					"required": []string{"id", "name", "provider", "code", "config", "registry", "assets"},
				},
			},
			"required": []string{"project"},
		}
	case "get_disk_usage_output":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"universe_name": map[string]interface{}{
					"type":        "string",
					"description": "Name of the universe (if querying specific universe)",
				},
				"disk_usage_bytes": map[string]interface{}{
					"type":        "integer",
					"description": "Disk usage in bytes",
				},
				"disk_usage_mb": map[string]interface{}{
					"type":        "integer",
					"description": "Disk usage in megabytes",
				},
				"disk_usage_gb": map[string]interface{}{
					"type":        "integer",
					"description": "Disk usage in gigabytes",
				},
			},
			"required": []string{"disk_usage_bytes", "disk_usage_mb", "disk_usage_gb"},
		}
	case "download_asset_output":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"success": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether the asset was downloaded successfully",
				},
				"file_path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the downloaded file",
				},
				"message": map[string]interface{}{
					"type":        "string",
					"description": "Status message",
				},
			},
			"required": []string{"success", "message"},
		}
	case "get_system_metrics_output":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cpu_usage_percent": map[string]interface{}{
					"type":        "number",
					"description": "CPU usage percentage",
				},
				"memory_usage_bytes": map[string]interface{}{
					"type":        "integer",
					"description": "Memory usage in bytes",
				},
				"memory_usage_mb": map[string]interface{}{
					"type":        "integer",
					"description": "Memory usage in megabytes",
				},
				"process_id": map[string]interface{}{
					"type":        "integer",
					"description": "Process ID of the Dream application",
				},
				"uptime": map[string]interface{}{
					"type":        "string",
					"description": "Application uptime as a duration string",
				},
			},
			"required": []string{"cpu_usage_percent", "memory_usage_bytes", "memory_usage_mb", "process_id", "uptime"},
		}
	case "get_dns_status_output":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"status": map[string]interface{}{
					"type":        "string",
					"description": "DNS service status",
				},
				"message": map[string]interface{}{
					"type":        "string",
					"description": "Status message",
				},
			},
			"required": []string{"status", "message"},
		}
	case "get_logs_output":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"logs": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"timestamp": map[string]interface{}{
								"type":        "string",
								"description": "Log entry timestamp",
							},
							"level": map[string]interface{}{
								"type":        "string",
								"description": "Log level",
							},
							"message": map[string]interface{}{
								"type":        "string",
								"description": "Log message",
							},
							"service": map[string]interface{}{
								"type":        "string",
								"description": "Service name that generated the log",
							},
						},
						"required": []string{"timestamp", "level", "message"},
					},
					"description": "List of log entries",
				},
			},
			"required": []string{"logs"},
		}
	}
	return nil
}
