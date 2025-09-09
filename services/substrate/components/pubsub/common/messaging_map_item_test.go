package common

import (
	"testing"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func TestMessagingMapItem_Len(t *testing.T) {
	mmi := &MessagingMapItem{}

	// Test empty map
	if mmi.Len() != 0 {
		t.Errorf("Expected length 0, got %d", mmi.Len())
	}

	// Test with items
	mmi.Items = []*MessagingItem{
		{project: "proj1", application: "app1", config: &structureSpec.Messaging{}},
		{project: "proj2", application: "app2", config: &structureSpec.Messaging{}},
	}

	if mmi.Len() != 2 {
		t.Errorf("Expected length 2, got %d", mmi.Len())
	}
}

func TestMessagingMapItem_Push(t *testing.T) {
	mmi := &MessagingMapItem{}

	// Test pushing to empty map
	project := "test-project"
	application := "test-application"
	config := &structureSpec.Messaging{
		Name:  "test-messaging",
		Match: "test-channel",
		Regex: false,
	}

	mmi.Push(project, application, config)

	if mmi.Len() != 1 {
		t.Errorf("Expected length 1, got %d", mmi.Len())
	}

	item := mmi.Items[0]
	if item.project != project {
		t.Errorf("Expected project %s, got %s", project, item.project)
	}
	if item.application != application {
		t.Errorf("Expected application %s, got %s", application, item.application)
	}
	if item.config != config {
		t.Errorf("Expected config %+v, got %+v", config, item.config)
	}

	// Test pushing multiple items
	mmi.Push("proj2", "app2", &structureSpec.Messaging{Name: "messaging2"})

	if mmi.Len() != 2 {
		t.Errorf("Expected length 2, got %d", mmi.Len())
	}
}

func TestMessagingMapItem_Matches_ExactMatch(t *testing.T) {
	mmi := &MessagingMapItem{}

	// Add items with exact matches
	mmi.Push("proj1", "app1", &structureSpec.Messaging{
		Name:  "messaging1",
		Match: "channel1",
		Regex: false,
	})
	mmi.Push("proj2", "app2", &structureSpec.Messaging{
		Name:  "messaging2",
		Match: "channel2",
		Regex: false,
	})

	// Test exact match
	matches := mmi.Matches("channel1")
	if len(matches) != 1 {
		t.Errorf("Expected 1 match, got %d", len(matches))
	}
	if matches[0].Name != "messaging1" {
		t.Errorf("Expected messaging1, got %s", matches[0].Name)
	}

	// Test no match
	matches = mmi.Matches("nonexistent")
	if len(matches) != 0 {
		t.Errorf("Expected 0 matches, got %d", len(matches))
	}
}

func TestMessagingMapItem_Matches_RegexMatch(t *testing.T) {
	mmi := &MessagingMapItem{}

	// Add items with regex matches
	mmi.Push("proj1", "app1", &structureSpec.Messaging{
		Name:  "messaging1",
		Match: "^test-.*",
		Regex: true,
	})
	mmi.Push("proj2", "app2", &structureSpec.Messaging{
		Name:  "messaging2",
		Match: ".*-channel$",
		Regex: true,
	})

	// Test regex match
	matches := mmi.Matches("test-something")
	if len(matches) != 1 {
		t.Errorf("Expected 1 match, got %d", len(matches))
	}
	if matches[0].Name != "messaging1" {
		t.Errorf("Expected messaging1, got %s", matches[0].Name)
	}

	// Test another regex match
	matches = mmi.Matches("my-channel")
	if len(matches) != 1 {
		t.Errorf("Expected 1 match, got %d", len(matches))
	}
	if matches[0].Name != "messaging2" {
		t.Errorf("Expected messaging2, got %s", matches[0].Name)
	}

	// Test no regex match
	matches = mmi.Matches("no-match")
	if len(matches) != 0 {
		t.Errorf("Expected 0 matches, got %d", len(matches))
	}
}

func TestMessagingMapItem_Matches_MixedExactAndRegex(t *testing.T) {
	mmi := &MessagingMapItem{}

	// Add mixed items
	mmi.Push("proj1", "app1", &structureSpec.Messaging{
		Name:  "exact-messaging",
		Match: "exact-channel",
		Regex: false,
	})
	mmi.Push("proj2", "app2", &structureSpec.Messaging{
		Name:  "regex-messaging",
		Match: ".*-test$",
		Regex: true,
	})

	// Test exact match
	matches := mmi.Matches("exact-channel")
	if len(matches) != 1 {
		t.Errorf("Expected 1 match, got %d", len(matches))
	}
	if matches[0].Name != "exact-messaging" {
		t.Errorf("Expected exact-messaging, got %s", matches[0].Name)
	}

	// Test regex match
	matches = mmi.Matches("something-test")
	if len(matches) != 1 {
		t.Errorf("Expected 1 match, got %d", len(matches))
	}
	if matches[0].Name != "regex-messaging" {
		t.Errorf("Expected regex-messaging, got %s", matches[0].Name)
	}
}

func TestMessagingMapItem_Matches_InvalidRegex(t *testing.T) {
	mmi := &MessagingMapItem{}

	// Add item with invalid regex
	mmi.Push("proj1", "app1", &structureSpec.Messaging{
		Name:  "invalid-regex",
		Match: "[invalid-regex",
		Regex: true,
	})

	// Should not panic and return no matches
	matches := mmi.Matches("test")
	if len(matches) != 0 {
		t.Errorf("Expected 0 matches for invalid regex, got %d", len(matches))
	}
}

func TestMessagingMapItem_Names(t *testing.T) {
	mmi := &MessagingMapItem{}

	// Test empty map
	names := mmi.Names()
	if len(names) != 0 {
		t.Errorf("Expected empty names, got %v", names)
	}

	// Add items
	mmi.Push("proj1", "app1", &structureSpec.Messaging{Name: "messaging1"})
	mmi.Push("proj2", "app2", &structureSpec.Messaging{Name: "messaging2"})
	mmi.Push("proj3", "app3", &structureSpec.Messaging{Name: "messaging3"})

	names = mmi.Names()
	expectedNames := []string{"messaging1", "messaging2", "messaging3"}

	if len(names) != len(expectedNames) {
		t.Errorf("Expected %d names, got %d", len(expectedNames), len(names))
	}

	for i, expectedName := range expectedNames {
		if names[i] != expectedName {
			t.Errorf("Expected name %s at index %d, got %s", expectedName, i, names[i])
		}
	}
}

func TestMessagingMapItem_Names_WithEmptyNames(t *testing.T) {
	mmi := &MessagingMapItem{}

	// Add items with empty names
	mmi.Push("proj1", "app1", &structureSpec.Messaging{Name: ""})
	mmi.Push("proj2", "app2", &structureSpec.Messaging{Name: "valid-name"})

	names := mmi.Names()
	expectedNames := []string{"", "valid-name"}

	if len(names) != len(expectedNames) {
		t.Errorf("Expected %d names, got %d", len(expectedNames), len(names))
	}

	for i, expectedName := range expectedNames {
		if names[i] != expectedName {
			t.Errorf("Expected name '%s' at index %d, got '%s'", expectedName, i, names[i])
		}
	}
}
