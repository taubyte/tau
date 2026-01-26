package raft

import (
	"errors"
	"testing"
)

func TestErrors_Defined(t *testing.T) {
	errs := []struct {
		name string
		err  error
	}{
		{"ErrNotLeader", ErrNotLeader},
		{"ErrNoLeader", ErrNoLeader},
		{"ErrShutdown", ErrShutdown},
		{"ErrInvalidCommand", ErrInvalidCommand},
		{"ErrAlreadyClosed", ErrAlreadyClosed},
		{"ErrInvalidNamespace", ErrInvalidNamespace},
	}

	for _, tc := range errs {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Errorf("%s is nil", tc.name)
			}
			if tc.err.Error() == "" {
				t.Errorf("%s has empty message", tc.name)
			}
		})
	}
}

func TestErrors_Unique(t *testing.T) {
	errs := []error{
		ErrNotLeader,
		ErrNoLeader,
		ErrShutdown,
		ErrInvalidCommand,
		ErrAlreadyClosed,
		ErrInvalidNamespace,
	}

	seen := make(map[string]bool)
	for _, err := range errs {
		msg := err.Error()
		if seen[msg] {
			t.Errorf("duplicate error message: %s", msg)
		}
		seen[msg] = true
	}
}

func TestErrors_Is(t *testing.T) {
	// Test that errors can be matched with errors.Is
	wrapped := errors.New("wrapped: " + ErrNotLeader.Error())

	// Direct comparison
	if !errors.Is(ErrNotLeader, ErrNotLeader) {
		t.Error("ErrNotLeader should match itself")
	}

	// Wrapped errors won't match with basic error wrapping
	// This is expected behavior with sentinel errors
	if errors.Is(wrapped, ErrNotLeader) {
		t.Error("wrapped error should not match (not using %w)")
	}
}
