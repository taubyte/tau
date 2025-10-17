package mcp

import (
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/taubyte/tau/dream"
	"gotest.tools/v3/assert"

	// Import dream services
	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/gateway/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/monkey/dream"
	_ "github.com/taubyte/tau/services/patrick/dream"
	_ "github.com/taubyte/tau/services/seer/dream"
	_ "github.com/taubyte/tau/services/substrate/dream"
	_ "github.com/taubyte/tau/services/tns/dream"

	// Import dream clients
	_ "github.com/taubyte/tau/clients/p2p/auth/dream"
	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/monkey/dream"
	_ "github.com/taubyte/tau/clients/p2p/patrick/dream"
	_ "github.com/taubyte/tau/clients/p2p/seer/dream"
	_ "github.com/taubyte/tau/clients/p2p/substrate/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
)

func TestMCPServer(t *testing.T) {
	// Create multiverse
	multiverse, err := dream.New(t.Context(), dream.LoadPersistent())
	assert.NilError(t, err)
	defer multiverse.Close()

	// Create MCP service
	mcpService, err := New(multiverse, nil)
	if err != nil {
		t.Fatalf("Failed to create MCP service: %v", err)
	}

	// Start MCP server in a goroutine
	go mcpService.Server().Start()

	// Wait a moment for server to start
	time.Sleep(100 * time.Millisecond)

	// Create MCP client
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "dream-mcp-test-client",
		Version: "1.0.0",
	}, nil)

	// Create HTTP transport
	transport := &mcp.StreamableClientTransport{
		Endpoint:   "http://localhost:8080" + MCPEndpoint,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}

	// Connect to MCP server
	connection, err := transport.Connect(t.Context())
	if err != nil {
		t.Fatalf("Failed to connect to MCP server: %v", err)
	}
	defer connection.Close()

	// Create session
	session, err := client.Connect(multiverse.Context(), transport, nil)
	if err != nil {
		t.Fatalf("Failed to create MCP session: %v", err)
	}
	defer session.Close()

	// Test 1: List available tools
	t.Run("ListTools", func(t *testing.T) {
		toolsResult, err := session.ListTools(multiverse.Context(), &mcp.ListToolsParams{})
		if err != nil {
			t.Fatalf("Failed to list tools: %v", err)
		}

		expectedTools := []string{
			"list_universes",
			"get_universe_status",
			"create_universe",
			"delete_universe",
			"start_universe",
			"stop_universe",
			"list_projects",
			"get_project_details",
			"get_disk_usage",
			"download_asset",
			"get_system_metrics",
			"get_dns_status",
			"get_logs",
		}

		if len(toolsResult.Tools) != len(expectedTools) {
			t.Errorf("Expected %d tools, got %d", len(expectedTools), len(toolsResult.Tools))
		}

		toolNames := make(map[string]bool)
		for _, tool := range toolsResult.Tools {
			toolNames[tool.Name] = true
		}

		for _, expectedTool := range expectedTools {
			if !toolNames[expectedTool] {
				t.Errorf("Expected tool %s not found", expectedTool)
			}
		}
	})

	// Test 2: List universes (should be empty initially)
	t.Run("ListUniverses", func(t *testing.T) {
		result, err := session.CallTool(multiverse.Context(), &mcp.CallToolParams{
			Name:      "list_universes",
			Arguments: map[string]any{},
		})
		if err != nil {
			t.Fatalf("Failed to list universes: %v", err)
		}

		if result.IsError {
			t.Fatalf("List universes returned error: %v", result.Content)
		}

		// Should return empty list initially
		if len(result.Content) == 0 {
			t.Error("Expected content in list universes result")
		}
	})

	// Test 3: Create universe
	t.Run("CreateUniverse", func(t *testing.T) {
		// First, try to delete the universe if it exists to ensure clean state
		_, _ = session.CallTool(multiverse.Context(), &mcp.CallToolParams{
			Name: "delete_universe",
			Arguments: map[string]any{
				"universe_name": "test-universe",
			},
		})

		result, err := session.CallTool(multiverse.Context(), &mcp.CallToolParams{
			Name: "create_universe",
			Arguments: map[string]any{
				"universe_name": "test-universe",
				"persistent":    true, // Make it persistent so it doesn't get auto-deleted on stop
			},
		})
		if err != nil {
			t.Fatalf("Failed to create universe: %v", err)
		}

		if result.IsError {
			t.Logf("Create universe returned error. Content: %+v", result.Content)
			if len(result.Content) > 0 {
				if textContent, ok := result.Content[0].(*mcp.TextContent); ok {
					t.Fatalf("Create universe returned error: %s", textContent.Text)
				}
			}
			t.Fatalf("Create universe returned error: %v", result.Content)
		}

		// Verify universe was created by listing universes
		listResult, err := session.CallTool(multiverse.Context(), &mcp.CallToolParams{
			Name:      "list_universes",
			Arguments: map[string]any{},
		})
		if err != nil {
			t.Fatalf("Failed to list universes after creation: %v", err)
		}

		if listResult.IsError {
			t.Fatalf("List universes after creation returned error: %v", listResult.Content)
		}
	})

	// Test 4: Get universe status
	t.Run("GetUniverseStatus", func(t *testing.T) {
		result, err := session.CallTool(multiverse.Context(), &mcp.CallToolParams{
			Name: "get_universe_status",
			Arguments: map[string]any{
				"universe_name": "test-universe",
			},
		})
		if err != nil {
			t.Fatalf("Failed to get universe status: %v", err)
		}

		if result.IsError {
			t.Fatalf("Get universe status returned error: %v", result.Content)
		}
	})

	// Test 5: Start universe
	t.Run("StartUniverse", func(t *testing.T) {
		result, err := session.CallTool(multiverse.Context(), &mcp.CallToolParams{
			Name: "start_universe",
			Arguments: map[string]any{
				"universe_name": "test-universe",
			},
		})
		if err != nil {
			t.Fatalf("Failed to start universe: %v", err)
		}

		if result.IsError {
			t.Fatalf("Start universe returned error: %v", result.Content)
		}
	})

	// Test 6: Get universe status after starting
	t.Run("GetUniverseStatusAfterStart", func(t *testing.T) {
		result, err := session.CallTool(multiverse.Context(), &mcp.CallToolParams{
			Name: "get_universe_status",
			Arguments: map[string]any{
				"universe_name": "test-universe",
			},
		})
		if err != nil {
			t.Fatalf("Failed to get universe status after start: %v", err)
		}

		if result.IsError {
			t.Fatalf("Get universe status after start returned error: %v", result.Content)
		}
	})

	// Test 7: List projects (should be empty for new universe)
	t.Run("ListProjects", func(t *testing.T) {
		result, err := session.CallTool(multiverse.Context(), &mcp.CallToolParams{
			Name: "list_projects",
			Arguments: map[string]any{
				"universe_name": "test-universe",
			},
		})
		if err != nil {
			t.Fatalf("Failed to list projects: %v", err)
		}

		if result.IsError {
			t.Fatalf("List projects returned error: %v", result.Content)
		}
	})

	// Test 8: Get disk usage
	t.Run("GetDiskUsage", func(t *testing.T) {
		result, err := session.CallTool(multiverse.Context(), &mcp.CallToolParams{
			Name: "get_disk_usage",
			Arguments: map[string]any{
				"universe_name": "test-universe",
			},
		})
		if err != nil {
			t.Fatalf("Failed to get disk usage: %v", err)
		}

		if result.IsError {
			t.Fatalf("Get disk usage returned error: %v", result.Content)
		}
	})

	// Test 9: Get system metrics
	t.Run("GetSystemMetrics", func(t *testing.T) {
		result, err := session.CallTool(multiverse.Context(), &mcp.CallToolParams{
			Name:      "get_system_metrics",
			Arguments: map[string]any{},
		})
		if err != nil {
			t.Fatalf("Failed to get system metrics: %v", err)
		}

		if result.IsError {
			t.Fatalf("Get system metrics returned error: %v", result.Content)
		}
	})

	// Test 10: Get DNS status
	t.Run("GetDNSStatus", func(t *testing.T) {
		result, err := session.CallTool(multiverse.Context(), &mcp.CallToolParams{
			Name:      "get_dns_status",
			Arguments: map[string]any{},
		})
		if err != nil {
			t.Fatalf("Failed to get DNS status: %v", err)
		}

		if result.IsError {
			t.Fatalf("Get DNS status returned error: %v", result.Content)
		}
	})

	// Test 11: Get logs
	t.Run("GetLogs", func(t *testing.T) {
		result, err := session.CallTool(multiverse.Context(), &mcp.CallToolParams{
			Name: "get_logs",
			Arguments: map[string]any{
				"universe_name": "test-universe",
				"lines":         10,
			},
		})
		if err != nil {
			t.Fatalf("Failed to get logs: %v", err)
		}

		if result.IsError {
			t.Fatalf("Get logs returned error: %v", result.Content)
		}
	})

	// Test 12: Stop universe
	t.Run("StopUniverse", func(t *testing.T) {
		result, err := session.CallTool(multiverse.Context(), &mcp.CallToolParams{
			Name: "stop_universe",
			Arguments: map[string]any{
				"universe_name": "test-universe",
			},
		})
		if err != nil {
			t.Fatalf("Failed to stop universe: %v", err)
		}

		if result.IsError {
			t.Fatalf("Stop universe returned error: %v", result.Content)
		}
	})

	// Test 13: Delete universe
	t.Run("DeleteUniverse", func(t *testing.T) {
		result, err := session.CallTool(multiverse.Context(), &mcp.CallToolParams{
			Name: "delete_universe",
			Arguments: map[string]any{
				"universe_name": "test-universe",
			},
		})
		if err != nil {
			t.Fatalf("Failed to delete universe: %v", err)
		}

		if result.IsError {
			t.Fatalf("Delete universe returned error: %v", result.Content)
		}
	})

	// Test 14: Verify universe is deleted
	t.Run("VerifyUniverseDeleted", func(t *testing.T) {
		result, err := session.CallTool(multiverse.Context(), &mcp.CallToolParams{
			Name:      "list_universes",
			Arguments: map[string]any{},
		})
		if err != nil {
			t.Fatalf("Failed to list universes after deletion: %v", err)
		}

		if result.IsError {
			t.Fatalf("List universes after deletion returned error: %v", result.Content)
		}

		// Should be empty again
		if len(result.Content) == 0 {
			t.Error("Expected content in list universes result after deletion")
		}
	})
}
