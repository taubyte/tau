package mcp

import (
	"fmt"
	"net/http"

	"github.com/google/jsonschema-go/jsonschema"
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
	DefaultPort = 8080
	// DefaultHost is the default host for the MCP service
	DefaultHost = "127.0.0.1"
	// MCPEndpoint is the HTTP endpoint path for MCP
	MCPEndpoint = "/mcp"
)

// Service represents the MCP server for Dream
type Service struct {
	server      *mcp.Server
	multiverse  *dream.Multiverse
	httpService httpIface.Service
}

// New creates a new MCP service using httpIface.Service
func New(multiverse *dream.Multiverse, httpService httpIface.Service) (*Service, error) {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    ServiceName,
		Title:   "Taubyte Dream MCP Server - Manage local Taubyte Clouds (Universes)",
		Version: ServiceVersion,
	}, nil)

	service := &Service{
		server:      server,
		multiverse:  multiverse,
		httpService: httpService,
	}

	if httpService == nil {
		var err error
		httpService, err = httpBasic.New(
			multiverse.Context(),
			options.Listen(fmt.Sprintf("%s:%d", DefaultHost, DefaultPort)),
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
func (m *Service) setupTransport() {
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return m.server
	}, &mcp.StreamableHTTPOptions{})

	m.httpService.LowLevelHandler(&httpIface.LowLevelHandlerDefinition{
		Path:    MCPEndpoint,
		Handler: handler,
	})
}

func (m *Service) registerTools() {
	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "list_universes",
		Description: "List all available universes in the multiverse (both persistent and temporary). Returns data with schema documentation included.",
		InputSchema: ListUniversesInputSchema,
	}, m.listUniverses)

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "get_universe_status",
		Description: "Get detailed status information for a specific universe. Returns data with schema documentation included.",
		InputSchema: GetUniverseStatusInputSchema,
	}, m.getUniverseStatus)

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "create_universe",
		Description: "Create a new universe with the specified name and persistence setting. Returns data with schema documentation included.",
		InputSchema: CreateUniverseInputSchema,
	}, m.createUniverse)

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "delete_universe",
		Description: "Delete an existing universe and clean up its resources. Returns data with schema documentation included.",
		InputSchema: DeleteUniverseInputSchema,
	}, m.deleteUniverse)

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "start_universe",
		Description: "Start a stopped universe. Returns data with schema documentation included.",
		InputSchema: StartUniverseInputSchema,
	}, m.startUniverse)

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "stop_universe",
		Description: "Stop a running universe. Returns data with schema documentation included.",
		InputSchema: StopUniverseInputSchema,
	}, m.stopUniverse)

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "list_projects",
		Description: "List all projects in a specific universe. Returns data with schema documentation included.",
		InputSchema: ListProjectsInputSchema,
		OutputSchema: &jsonschema.Schema{
			Type: "object",
		},
	}, m.listProjects)

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "get_project_details",
		Description: "Get detailed information about a specific project. Returns data with schema documentation included.",
		InputSchema: GetProjectDetailsInputSchema,
		OutputSchema: &jsonschema.Schema{
			Type: "object",
		},
	}, m.getProjectDetails)

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "get_disk_usage",
		Description: "Get disk usage information for a universe or overall system. Returns data with schema documentation included.",
		InputSchema: GetDiskUsageInputSchema,
		OutputSchema: &jsonschema.Schema{
			Type: "object",
		},
	}, m.getDiskUsage)

}

// Server returns the underlying httpIface.Service
func (m *Service) Server() httpIface.Service {
	return m.httpService
}
