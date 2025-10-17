package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/taubyte/tau/dream"
	httpIface "github.com/taubyte/tau/pkg/http"
	httpBasic "github.com/taubyte/tau/pkg/http/basic"
	"github.com/taubyte/tau/pkg/http/options"
)

const (
	// ServiceName is the name of the MCP service
	ServiceName = "dream"
	// ServiceVersion is the version of the MCP service
	ServiceVersion = "1.0.0"
	// DefaultPort is the default HTTP port for the MCP service
	DefaultPort = ":8080"
	// MCPEndpoint is the HTTP endpoint path for MCP
	MCPEndpoint = "/mcp"
)

// MCPService represents the MCP server for Dream
type MCPService struct {
	server      *mcp.Server
	multiverse  *dream.Multiverse
	httpService httpIface.Service
}

// New creates a new MCP service using httpIface.Service
func New(multiverse *dream.Multiverse, httpService httpIface.Service) (*MCPService, error) {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    ServiceName,
		Version: ServiceVersion,
	}, nil)

	service := &MCPService{
		server:      server,
		multiverse:  multiverse,
		httpService: httpService,
	}

	// If no httpService provided, create a new one
	if httpService == nil {
		var err error
		httpService, err = httpBasic.New(
			multiverse.Context(),
			options.Listen(DefaultPort),
			options.AllowedOrigins(true, []string{".*"}),
		)
		if err != nil {
			return nil, err
		}
		service.httpService = httpService
	}

	service.registerTools()
	service.setupTransport()

	return service, nil
}

// setupTransport sets up the MCP transport using LowLevelHandler
func (m *MCPService) setupTransport() {
	// Create the MCP HTTP handler using the official StreamableHTTPHandler
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return m.server
	}, &mcp.StreamableHTTPOptions{})

	// Use LowLevelHandler to register the MCP endpoint
	m.httpService.LowLevelHandler(&httpIface.LowLevelHandlerDefinition{
		Path:    MCPEndpoint,
		Handler: handler,
	})
}

// registerTools registers all available MCP tools
func (m *MCPService) registerTools() {
	// Universe management tools
	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "list_universes",
		Description: "List all available universes in the multiverse (both persistent and temporary). Returns data with schema documentation included.",
	}, m.listUniverses)

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "get_universe_status",
		Description: "Get detailed status information for a specific universe. Returns data with schema documentation included.",
	}, m.getUniverseStatus)

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "create_universe",
		Description: "Create a new universe with the specified name and persistence setting. Returns data with schema documentation included.",
	}, m.createUniverse)

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "delete_universe",
		Description: "Delete an existing universe and clean up its resources. Returns data with schema documentation included.",
	}, m.deleteUniverse)

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "start_universe",
		Description: "Start a stopped universe. Returns data with schema documentation included.",
	}, m.startUniverse)

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "stop_universe",
		Description: "Stop a running universe. Returns data with schema documentation included.",
	}, m.stopUniverse)

	// Project management tools
	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "list_projects",
		Description: "List all projects in a specific universe. Returns data with schema documentation included.",
	}, m.listProjects)

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "get_project_details",
		Description: "Get detailed information about a specific project. Returns data with schema documentation included.",
	}, m.getProjectDetails)

	// Resource management tools
	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "get_disk_usage",
		Description: "Get disk usage information for a universe or overall system. Returns data with schema documentation included.",
	}, m.getDiskUsage)

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "download_asset",
		Description: "Download an asset from a universe to local filesystem",
	}, m.downloadAsset)

	// System monitoring tools
	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "get_system_metrics",
		Description: "Get system metrics including CPU, memory, and process information. Returns data with schema documentation included.",
	}, m.getSystemMetrics)

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "get_dns_status",
		Description: "Get DNS service status and configuration",
	}, m.getDNSStatus)

	// Logging tools
	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "get_logs",
		Description: "Get logs for a specific universe or service",
	}, m.getLogs)
}

// Server returns the underlying httpIface.Service
func (m *MCPService) Server() httpIface.Service {
	return m.httpService
}
