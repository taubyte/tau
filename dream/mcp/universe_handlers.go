package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/taubyte/tau/dream"
)

// Universe management tool implementations

func (m *MCPService) listUniverses(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, ListUniversesOutput, error) {
	// Get all universes from multiverse
	universeNames, err := m.multiverse.List()
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error listing universes: %v", err)}}}, ListUniversesOutput{}, nil
	}

	universes := make([]UniverseInfo, 0, len(universeNames))
	for _, name := range universeNames {
		// Get universe instance to check status and persistence
		universe, err := m.multiverse.Universe(name)
		if err != nil {
			// If we can't get the universe, it's probably stopped
			universes = append(universes, UniverseInfo{
				Name:       name,
				Status:     "stopped",
				Persistent: false,
			})
			continue
		}

		// Check if universe is running
		status := "stopped"
		if universe != nil && universe.Running() {
			status = "started"
		}

		// Get persistence
		persistent := false
		if universe != nil {
			persistent = universe.Persistent()
		}

		universes = append(universes, UniverseInfo{
			Name:       name,
			Status:     status,
			Persistent: persistent,
		})
	}

	return nil, ListUniversesOutput{
		Universes: universes,
		Schema:    m.getSchemaForType("list_universes_output"),
	}, nil
}

func (m *MCPService) getUniverseStatus(ctx context.Context, req *mcp.CallToolRequest, input GetUniverseStatusInput) (*mcp.CallToolResult, GetUniverseStatusOutput, error) {
	universe, err := m.multiverse.Universe(input.UniverseName)
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("universe %s not found: %v", input.UniverseName, err)}}}, GetUniverseStatusOutput{}, nil
	}

	// Get universe status from multiverse
	status := m.multiverse.Status()
	universeStatus, exists := status[input.UniverseName]
	if !exists {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("universe %s not found in status", input.UniverseName)}}}, GetUniverseStatusOutput{}, nil
	}

	// Check if universe is started
	statusStr := "stopped"
	if universe != nil {
		statusStr = "started"
	}

	// Get ports from universe nodes
	var ports []int
	if universe != nil {
		portSet := make(map[int]bool)

		// Get seer ports (DNS)
		if seerNode := universe.Seer(); seerNode != nil {
			if seerInfo, err := universe.GetInfo(seerNode.Node()); err == nil {
				if dnsPort, ok := seerInfo.Ports["dns"]; ok {
					portSet[dnsPort] = true
				}
			}
		}

		// Get substrate ports (HTTP)
		if substrateNode := universe.Substrate(); substrateNode != nil {
			if substrateInfo, err := universe.GetInfo(substrateNode.Node()); err == nil {
				if httpPort, ok := substrateInfo.Ports["http"]; ok {
					portSet[httpPort] = true
				}
			}
		}

		// Get gateway ports (HTTP)
		if gatewayNode := universe.Gateway(); gatewayNode != nil {
			if gatewayInfo, err := universe.GetInfo(gatewayNode.Node()); err == nil {
				if httpPort, ok := gatewayInfo.Ports["http"]; ok {
					portSet[httpPort] = true
				}
			}
		}

		// Convert set to slice
		for port := range portSet {
			ports = append(ports, port)
		}
	}

	// Get disk usage and persistence
	var diskUsage int64
	var persistent bool
	if universe != nil {
		if usage, err := universe.DiskUsage(); err == nil {
			diskUsage = usage
		}
		persistent = universe.Persistent()
	}

	return nil, GetUniverseStatusOutput{
		Name:       input.UniverseName,
		Status:     statusStr,
		Root:       universeStatus.Root,
		SwarmKey:   string(universeStatus.SwarmKey),
		NodeCount:  universeStatus.NodeCount,
		Simples:    universeStatus.Simples,
		Nodes:      universeStatus.Nodes,
		Services:   universeStatus.Services,
		Ports:      ports,
		DiskUsage:  diskUsage,
		Persistent: persistent,
		Schema:     m.getSchemaForType("get_universe_status_output"),
	}, nil
}

func (m *MCPService) createUniverse(ctx context.Context, req *mcp.CallToolRequest, input CreateUniverseInput) (*mcp.CallToolResult, CreateUniverseOutput, error) {
	// Check if universe already exists
	_, err := m.multiverse.Universe(input.UniverseName)
	if err == nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("universe %s already exists", input.UniverseName)}}}, CreateUniverseOutput{}, nil
	}

	// Create universe using multiverse
	config := dream.UniverseConfig{
		Name:     input.UniverseName,
		KeepRoot: input.Persistent,
	}
	_, err = m.multiverse.New(config)
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("failed to create universe %s: %v", input.UniverseName, err)}}}, CreateUniverseOutput{}, nil
	}

	// Universe is created and ready to use (not started yet)

	return nil, CreateUniverseOutput{
		Success:      true,
		UniverseName: input.UniverseName,
		Message:      fmt.Sprintf("Universe '%s' created successfully", input.UniverseName),
		Schema:       m.getSchemaForType("create_universe_output"),
	}, nil
}

func (m *MCPService) deleteUniverse(ctx context.Context, req *mcp.CallToolRequest, input DeleteUniverseInput) (*mcp.CallToolResult, DeleteUniverseOutput, error) {
	// Delete universe using multiverse
	err := m.multiverse.Delete(input.UniverseName)
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("failed to delete universe %s: %v", input.UniverseName, err)}}}, DeleteUniverseOutput{}, nil
	}

	return nil, DeleteUniverseOutput{
		Success: true,
		Message: fmt.Sprintf("Universe '%s' deleted successfully", input.UniverseName),
		Schema:  m.getSchemaForType("delete_universe_output"),
	}, nil
}

func (m *MCPService) startUniverse(ctx context.Context, req *mcp.CallToolRequest, input StartUniverseInput) (*mcp.CallToolResult, StartUniverseOutput, error) {
	universe, err := m.multiverse.Universe(input.UniverseName)
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("universe %s not found: %v", input.UniverseName, err)}}}, StartUniverseOutput{}, nil
	}

	// Start the universe
	err = universe.StartAll()
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("failed to start universe %s: %v", input.UniverseName, err)}}}, StartUniverseOutput{}, nil
	}

	return nil, StartUniverseOutput{
		Success:      true,
		UniverseName: input.UniverseName,
		Message:      fmt.Sprintf("Universe '%s' started successfully", input.UniverseName),
		Schema:       m.getSchemaForType("start_universe_output"),
	}, nil
}

func (m *MCPService) stopUniverse(ctx context.Context, req *mcp.CallToolRequest, input StopUniverseInput) (*mcp.CallToolResult, StopUniverseOutput, error) {
	universe, err := m.multiverse.Universe(input.UniverseName)
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("universe %s not found: %v", input.UniverseName, err)}}}, StopUniverseOutput{}, nil
	}

	// Stop the universe
	universe.Stop()

	return nil, StopUniverseOutput{
		Success:      true,
		UniverseName: input.UniverseName,
		Message:      fmt.Sprintf("Universe '%s' stopped successfully", input.UniverseName),
		Schema:       m.getSchemaForType("stop_universe_output"),
	}, nil
}
