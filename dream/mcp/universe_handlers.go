package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/taubyte/tau/dream"
)

func (m *Service) listUniverses(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, ListUniversesOutput, error) {
	universeNames, err := m.multiverse.List()
	if err != nil {
		return errorListUniverses("Error listing universes: %v", err)
	}

	universes := make([]UniverseInfo, 0, len(universeNames))
	for _, name := range universeNames {
		universe, err := m.multiverse.Universe(name)
		if err != nil {
			universes = append(universes, UniverseInfo{
				Name:       name,
				Status:     "stopped",
				Persistent: false,
			})
			continue
		}

		status := dream.UniverseStateStopped
		if universe != nil {
			status = universe.State()
		}

		persistent := false
		if universe != nil {
			persistent = universe.Persistent()
		}

		universes = append(universes, UniverseInfo{
			Name:       name,
			Status:     status.String(),
			Persistent: persistent,
		})
	}

	return nil, ListUniversesOutput{
		Universes: universes,
	}, nil
}

func (m *Service) getUniverseStatus(ctx context.Context, req *mcp.CallToolRequest, input GetUniverseStatusInput) (*mcp.CallToolResult, GetUniverseStatusOutput, error) {
	universe, err := m.multiverse.Universe(input.UniverseName)
	if err != nil {
		return errorGetUniverseStatus("universe %s not found: %v", input.UniverseName, err)
	}

	status := m.multiverse.Status()
	universeStatus, exists := status[input.UniverseName]
	if !exists {
		return errorGetUniverseStatus("universe %s not found in status", input.UniverseName)
	}

	state := dream.UniverseStateStopped
	if universe != nil {
		state = universe.State()
	}

	var ports []int
	if universe != nil {
		portSet := make(map[int]bool)

		if seerNode := universe.Seer(); seerNode != nil {
			if seerInfo, err := universe.GetInfo(seerNode.Node()); err == nil {
				if dnsPort, ok := seerInfo.Ports["dns"]; ok {
					portSet[dnsPort] = true
				}
			}
		}

		if substrateNode := universe.Substrate(); substrateNode != nil {
			if substrateInfo, err := universe.GetInfo(substrateNode.Node()); err == nil {
				if httpPort, ok := substrateInfo.Ports["http"]; ok {
					portSet[httpPort] = true
				}
			}
		}

		if gatewayNode := universe.Gateway(); gatewayNode != nil {
			if gatewayInfo, err := universe.GetInfo(gatewayNode.Node()); err == nil {
				if httpPort, ok := gatewayInfo.Ports["http"]; ok {
					portSet[httpPort] = true
				}
			}
		}

		for port := range portSet {
			ports = append(ports, port)
		}
	}

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
		Status:     state.String(),
		Root:       universeStatus.Root,
		SwarmKey:   string(universeStatus.SwarmKey),
		NodeCount:  universeStatus.NodeCount,
		Simples:    universeStatus.Simples,
		Nodes:      universeStatus.Nodes,
		Services:   universeStatus.Services,
		Ports:      ports,
		DiskUsage:  diskUsage,
		Persistent: persistent,
	}, nil
}

func (m *Service) createUniverse(ctx context.Context, req *mcp.CallToolRequest, input CreateUniverseInput) (*mcp.CallToolResult, CreateUniverseOutput, error) {
	_, err := m.multiverse.Universe(input.UniverseName)
	if err == nil {
		return errorCreateUniverse("universe %s already exists", input.UniverseName)
	}

	config := dream.UniverseConfig{
		Name:     input.UniverseName,
		KeepRoot: input.Persistent,
	}
	_, err = m.multiverse.New(config)
	if err != nil {
		return errorCreateUniverse("failed to create universe %s: %v", input.UniverseName, err)
	}

	return nil, CreateUniverseOutput{
		Success:      true,
		UniverseName: input.UniverseName,
		Message:      fmt.Sprintf("Universe '%s' created successfully", input.UniverseName),
	}, nil
}

func (m *Service) deleteUniverse(ctx context.Context, req *mcp.CallToolRequest, input DeleteUniverseInput) (*mcp.CallToolResult, DeleteUniverseOutput, error) {
	err := m.multiverse.Delete(input.UniverseName)
	if err != nil {
		return errorDeleteUniverse("failed to delete universe %s: %v", input.UniverseName, err)
	}

	return nil, DeleteUniverseOutput{
		Success: true,
		Message: fmt.Sprintf("Universe '%s' deleted successfully", input.UniverseName),
	}, nil
}

func (m *Service) startUniverse(ctx context.Context, req *mcp.CallToolRequest, input StartUniverseInput) (*mcp.CallToolResult, StartUniverseOutput, error) {
	universe, err := m.multiverse.Universe(input.UniverseName)
	if err != nil {
		return errorStartUniverse("universe %s not found: %v", input.UniverseName, err)
	}

	err = universe.StartAll("client")
	if err != nil {
		return errorStartUniverse("failed to start universe %s: %v", input.UniverseName, err)
	}

	return nil, StartUniverseOutput{
		Success:      true,
		UniverseName: input.UniverseName,
		Message:      fmt.Sprintf("Universe '%s' started successfully", input.UniverseName),
	}, nil
}

func (m *Service) stopUniverse(ctx context.Context, req *mcp.CallToolRequest, input StopUniverseInput) (*mcp.CallToolResult, StopUniverseOutput, error) {
	universe, err := m.multiverse.Universe(input.UniverseName)
	if err != nil {
		return errorStopUniverse("universe %s not found: %v", input.UniverseName, err)
	}

	universe.Stop()

	return nil, StopUniverseOutput{
		Success:      true,
		UniverseName: input.UniverseName,
		Message:      fmt.Sprintf("Universe '%s' stopped successfully", input.UniverseName),
	}, nil
}
