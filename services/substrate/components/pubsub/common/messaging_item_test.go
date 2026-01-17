package common

import (
	"testing"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func TestMessagingItem_Project(t *testing.T) {
	expectedProject := "test-project"
	item := &MessagingItem{
		project:     expectedProject,
		application: "test-app",
		config:      &structureSpec.Messaging{},
	}

	if item.Project() != expectedProject {
		t.Errorf("Expected project %s, got %s", expectedProject, item.Project())
	}
}

func TestMessagingItem_Application(t *testing.T) {
	expectedApplication := "test-application"
	item := &MessagingItem{
		project:     "test-project",
		application: expectedApplication,
		config:      &structureSpec.Messaging{},
	}

	if item.Application() != expectedApplication {
		t.Errorf("Expected application %s, got %s", expectedApplication, item.Application())
	}
}

func TestMessagingItem_Config(t *testing.T) {
	expectedConfig := &structureSpec.Messaging{
		Name:  "test-messaging",
		Match: "test-channel",
		Regex: false,
	}

	item := &MessagingItem{
		project:     "test-project",
		application: "test-app",
		config:      expectedConfig,
	}

	config := item.Config()
	if config != expectedConfig {
		t.Errorf("Expected config %+v, got %+v", expectedConfig, config)
	}

	// Test that the config is the same reference
	if config.Name != "test-messaging" {
		t.Errorf("Expected config name 'test-messaging', got %s", config.Name)
	}
}

func TestMessagingItem_EmptyConfig(t *testing.T) {
	item := &MessagingItem{
		project:     "test-project",
		application: "test-app",
		config:      nil,
	}

	config := item.Config()
	if config != nil {
		t.Errorf("Expected nil config, got %+v", config)
	}
}
