package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (m *Service) getDiskUsage(ctx context.Context, req *mcp.CallToolRequest, input GetDiskUsageInput) (*mcp.CallToolResult, GetDiskUsageOutput, error) {
	if input.UniverseName == "" {
		return errorGetDiskUsage("universe_name is required")
	}

	universe, err := m.multiverse.Universe(input.UniverseName)
	if err != nil {
		return errorGetDiskUsage("universe %s not found: %v", input.UniverseName, err)
	}

	diskUsage, err := universe.DiskUsage()
	if err != nil {
		return errorGetDiskUsage("failed to calculate disk usage for universe %s: %v", input.UniverseName, err)
	}

	return nil, GetDiskUsageOutput{
		UniverseName: input.UniverseName,
		DiskUsage:    diskUsage,
		DiskUsageMB:  diskUsage / (1024 * 1024),
		DiskUsageGB:  diskUsage / (1024 * 1024 * 1024),
		Schema:       GetDiskUsageOutputSchema,
	}, nil
}
