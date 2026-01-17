package common

import (
	"testing"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func TestMessagingMap_Initialization(t *testing.T) {
	mm := &MessagingMap{}

	// Test initial state
	if mm.Function.Len() != 0 {
		t.Errorf("Expected Function length 0, got %d", mm.Function.Len())
	}
	if mm.HasAny {
		t.Errorf("Expected HasAny to be false initially, got true")
	}
}

func TestMessagingMap_FunctionOperations(t *testing.T) {
	mm := &MessagingMap{}

	// Add function messaging
	mm.Function.Push("proj1", "app1", &structureSpec.Messaging{
		Name:  "function-messaging",
		Match: "function-channel",
		Regex: false,
	})

	if mm.Function.Len() != 1 {
		t.Errorf("Expected Function length 1, got %d", mm.Function.Len())
	}

	// Test function matches
	matches := mm.Function.Matches("function-channel")
	if len(matches) != 1 {
		t.Errorf("Expected 1 function match, got %d", len(matches))
	}
	if matches[0].Name != "function-messaging" {
		t.Errorf("Expected function-messaging, got %s", matches[0].Name)
	}
}

func TestMessagingMap_MixedOperations(t *testing.T) {
	mm := &MessagingMap{}

	// Add both function and websocket messaging
	mm.Function.Push("proj1", "app1", &structureSpec.Messaging{
		Name:  "function-messaging",
		Match: "shared-channel",
		Regex: false,
	})

	// Test function matches
	functionMatches := mm.Function.Matches("shared-channel")
	if len(functionMatches) != 1 {
		t.Errorf("Expected 1 function match, got %d", len(functionMatches))
	}
	if functionMatches[0].Name != "function-messaging" {
		t.Errorf("Expected function-messaging, got %s", functionMatches[0].Name)
	}

}

func TestMessagingMap_HasAnyFlag(t *testing.T) {
	mm := &MessagingMap{}

	// Initially should be false
	if mm.HasAny {
		t.Errorf("Expected HasAny to be false initially, got true")
	}

	// Set to true
	mm.HasAny = true
	if !mm.HasAny {
		t.Errorf("Expected HasAny to be true after setting, got false")
	}

	// Set back to false
	mm.HasAny = false
	if mm.HasAny {
		t.Errorf("Expected HasAny to be false after resetting, got true")
	}
}

func TestMessagingMap_ComplexScenario(t *testing.T) {
	mm := &MessagingMap{}

	// Add multiple function messagings
	mm.Function.Push("proj1", "app1", &structureSpec.Messaging{
		Name:  "function1",
		Match: "test-.*",
		Regex: true,
	})
	mm.Function.Push("proj2", "app2", &structureSpec.Messaging{
		Name:  "function2",
		Match: "exact-channel",
		Regex: false,
	})

	// Set HasAny flag
	mm.HasAny = true

	// Test function regex match
	functionMatches := mm.Function.Matches("test-something")
	if len(functionMatches) != 1 {
		t.Errorf("Expected 1 function regex match, got %d", len(functionMatches))
	}
	if functionMatches[0].Name != "function1" {
		t.Errorf("Expected function1, got %s", functionMatches[0].Name)
	}

	// Test function exact match
	functionMatches = mm.Function.Matches("exact-channel")
	if len(functionMatches) != 1 {
		t.Errorf("Expected 1 function exact match, got %d", len(functionMatches))
	}
	if functionMatches[0].Name != "function2" {
		t.Errorf("Expected function2, got %s", functionMatches[0].Name)
	}

	// Test no matches
	functionMatches = mm.Function.Matches("no-match")
	if len(functionMatches) != 0 {
		t.Errorf("Expected 0 function matches, got %d", len(functionMatches))
	}

	// Verify lengths
	if mm.Function.Len() != 2 {
		t.Errorf("Expected Function length 2, got %d", mm.Function.Len())
	}
	if !mm.HasAny {
		t.Errorf("Expected HasAny to be true, got false")
	}
}
