package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Project management tool implementations

func (m *MCPService) listProjects(ctx context.Context, req *mcp.CallToolRequest, input ListProjectsInput) (*mcp.CallToolResult, ListProjectsOutput, error) {
	// Check if universe exists
	_, err := m.multiverse.Universe(input.UniverseName)
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("universe %s not found: %v", input.UniverseName, err)}}}, ListProjectsOutput{Projects: []ProjectInfo{}}, nil
	}

	// For now, return empty list - projects functionality would need to be implemented
	// based on the actual dream project management system
	return nil, ListProjectsOutput{
		Projects: []ProjectInfo{},
		Schema:   m.getSchemaForType("list_projects_output"),
	}, nil
}

func (m *MCPService) getProjectDetails(ctx context.Context, req *mcp.CallToolRequest, input GetProjectDetailsInput) (*mcp.CallToolResult, GetProjectDetailsOutput, error) {
	// Check if universe exists
	_, err := m.multiverse.Universe(input.UniverseName)
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("universe %s not found: %v", input.UniverseName, err)}}}, GetProjectDetailsOutput{}, nil
	}

	// For now, return empty project - projects functionality would need to be implemented
	// based on the actual dream project management system
	return nil, GetProjectDetailsOutput{
		Project: ProjectInfo{},
		Schema:  m.getSchemaForType("get_project_details_output"),
	}, nil
}
