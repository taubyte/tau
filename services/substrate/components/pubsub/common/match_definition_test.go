package common

import (
	"testing"
)

func TestMatchDefinition_String(t *testing.T) {
	md := &MatchDefinition{
		Channel:     "test-channel",
		Project:     "test-project",
		Application: "test-application",
		WebSocket:   false,
	}

	result := md.String()

	// The result should be a hash of project+application + "/" + channel
	// We can't predict the exact hash, but we can verify the format
	if result == "" {
		t.Errorf("Expected non-empty string, got empty")
	}

	// Should contain the channel at the end
	if len(result) <= len(md.Channel) {
		t.Errorf("Expected result to be longer than channel, got %s", result)
	}

	// Should end with the channel
	if result[len(result)-len(md.Channel):] != md.Channel {
		t.Errorf("Expected result to end with channel '%s', got '%s'", md.Channel, result)
	}
}

func TestMatchDefinition_String_DifferentInputs(t *testing.T) {
	testCases := []struct {
		name        string
		channel     string
		project     string
		application string
		websocket   bool
	}{
		{
			name:        "basic",
			channel:     "channel1",
			project:     "project1",
			application: "app1",
			websocket:   false,
		},
		{
			name:        "empty strings",
			channel:     "",
			project:     "",
			application: "",
			websocket:   false,
		},
		{
			name:        "special characters",
			channel:     "test-channel_123",
			project:     "project@domain.com",
			application: "app-v2.0",
			websocket:   true,
		},
		{
			name:        "unicode",
			channel:     "测试频道",
			project:     "项目",
			application: "应用",
			websocket:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			md := &MatchDefinition{
				Channel:     tc.channel,
				Project:     tc.project,
				Application: tc.application,
				WebSocket:   tc.websocket,
			}

			result := md.String()

			// Should not be empty
			if result == "" {
				t.Errorf("Expected non-empty string for case %s, got empty", tc.name)
			}

			// Should end with the channel (if channel is not empty)
			if tc.channel != "" && len(result) > len(tc.channel) {
				if result[len(result)-len(tc.channel):] != tc.channel {
					t.Errorf("Expected result to end with channel '%s' for case %s, got '%s'", tc.channel, tc.name, result)
				}
			}
		})
	}
}

func TestMatchDefinition_CachePrefix(t *testing.T) {
	md := &MatchDefinition{
		Channel:     "test-channel",
		Project:     "test-project",
		Application: "test-application",
		WebSocket:   false,
	}

	result := md.CachePrefix()
	expected := "test-project"

	if result != expected {
		t.Errorf("Expected cache prefix '%s', got '%s'", expected, result)
	}
}

func TestMatchDefinition_CachePrefix_EmptyProject(t *testing.T) {
	md := &MatchDefinition{
		Channel:     "test-channel",
		Project:     "",
		Application: "test-application",
		WebSocket:   false,
	}

	result := md.CachePrefix()
	expected := ""

	if result != expected {
		t.Errorf("Expected empty cache prefix, got '%s'", result)
	}
}

func TestMatchDefinition_GenerateSocketURL(t *testing.T) {
	md := &MatchDefinition{
		Channel:     "test-channel",
		Project:     "test-project",
		Application: "test-application",
		WebSocket:   false,
	}

	result := md.GenerateSocketURL()

	// Should start with "ws-"
	if len(result) < 3 || result[:3] != "ws-" {
		t.Errorf("Expected result to start with 'ws-', got '%s'", result)
	}

	// Should contain the channel at the end
	if len(result) <= len(md.Channel) {
		t.Errorf("Expected result to be longer than channel, got '%s'", result)
	}

	// Should end with the channel
	if result[len(result)-len(md.Channel):] != md.Channel {
		t.Errorf("Expected result to end with channel '%s', got '%s'", md.Channel, result)
	}
}

func TestMatchDefinition_GenerateSocketURL_DifferentInputs(t *testing.T) {
	testCases := []struct {
		name        string
		channel     string
		project     string
		application string
		websocket   bool
	}{
		{
			name:        "basic",
			channel:     "channel1",
			project:     "project1",
			application: "app1",
			websocket:   false,
		},
		{
			name:        "websocket true",
			channel:     "ws-channel",
			project:     "ws-project",
			application: "ws-app",
			websocket:   true,
		},
		{
			name:        "empty channel",
			channel:     "",
			project:     "project1",
			application: "app1",
			websocket:   false,
		},
		{
			name:        "special characters",
			channel:     "test-channel_123",
			project:     "project@domain.com",
			application: "app-v2.0",
			websocket:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			md := &MatchDefinition{
				Channel:     tc.channel,
				Project:     tc.project,
				Application: tc.application,
				WebSocket:   tc.websocket,
			}

			result := md.GenerateSocketURL()

			// Should start with "ws-"
			if len(result) < 3 || result[:3] != "ws-" {
				t.Errorf("Expected result to start with 'ws-' for case %s, got '%s'", tc.name, result)
			}

			// Should end with the channel (if channel is not empty)
			if tc.channel != "" && len(result) > len(tc.channel) {
				if result[len(result)-len(tc.channel):] != tc.channel {
					t.Errorf("Expected result to end with channel '%s' for case %s, got '%s'", tc.channel, tc.name, result)
				}
			}
		})
	}
}

func TestMatchDefinition_Consistency(t *testing.T) {
	md := &MatchDefinition{
		Channel:     "test-channel",
		Project:     "test-project",
		Application: "test-application",
		WebSocket:   false,
	}

	// String() and GenerateSocketURL() should be consistent
	stringResult := md.String()
	socketURL := md.GenerateSocketURL()

	// Socket URL should be String() with "ws-" prefix
	expectedSocketURL := "ws-" + stringResult
	if socketURL != expectedSocketURL {
		t.Errorf("Expected socket URL '%s', got '%s'", expectedSocketURL, socketURL)
	}
}

func TestMatchDefinition_FieldValues(t *testing.T) {
	md := &MatchDefinition{
		Channel:     "test-channel",
		Project:     "test-project",
		Application: "test-application",
		WebSocket:   true,
	}

	// Test field values are preserved
	if md.Channel != "test-channel" {
		t.Errorf("Expected channel 'test-channel', got '%s'", md.Channel)
	}
	if md.Project != "test-project" {
		t.Errorf("Expected project 'test-project', got '%s'", md.Project)
	}
	if md.Application != "test-application" {
		t.Errorf("Expected application 'test-application', got '%s'", md.Application)
	}
	if !md.WebSocket {
		t.Errorf("Expected WebSocket to be true, got false")
	}
}
