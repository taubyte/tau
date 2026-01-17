package mcp

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func errorResult[T any](retValue T, format string, args ...interface{}) (*mcp.CallToolResult, T, error) {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}}}, retValue, nil
}

func errorListProjects(format string, args ...interface{}) (*mcp.CallToolResult, ListProjectsOutput, error) {
	return errorResult(ListProjectsOutput{Projects: []ProjectInfo{}}, format, args...)
}

func errorGetProjectDetails(format string, args ...interface{}) (*mcp.CallToolResult, GetProjectDetailsOutput, error) {
	return errorResult(GetProjectDetailsOutput{}, format, args...)
}

func errorListUniverses(format string, args ...interface{}) (*mcp.CallToolResult, ListUniversesOutput, error) {
	return errorResult(ListUniversesOutput{}, format, args...)
}

func errorGetUniverseStatus(format string, args ...interface{}) (*mcp.CallToolResult, GetUniverseStatusOutput, error) {
	return errorResult(GetUniverseStatusOutput{}, format, args...)
}

func errorCreateUniverse(format string, args ...interface{}) (*mcp.CallToolResult, CreateUniverseOutput, error) {
	return errorResult(CreateUniverseOutput{}, format, args...)
}

func errorDeleteUniverse(format string, args ...interface{}) (*mcp.CallToolResult, DeleteUniverseOutput, error) {
	return errorResult(DeleteUniverseOutput{}, format, args...)
}

func errorStartUniverse(format string, args ...interface{}) (*mcp.CallToolResult, StartUniverseOutput, error) {
	return errorResult(StartUniverseOutput{}, format, args...)
}

func errorStopUniverse(format string, args ...interface{}) (*mcp.CallToolResult, StopUniverseOutput, error) {
	return errorResult(StopUniverseOutput{}, format, args...)
}

func errorGetDiskUsage(format string, args ...interface{}) (*mcp.CallToolResult, GetDiskUsageOutput, error) {
	return errorResult(GetDiskUsageOutput{}, format, args...)
}
