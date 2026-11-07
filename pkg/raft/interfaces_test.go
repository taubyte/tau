package raft

import (
	"testing"
)

func TestFSMResponse(t *testing.T) {
	resp := FSMResponse{
		Error: nil,
		Data:  []byte("test"),
	}

	if resp.Error != nil {
		t.Error("expected nil error")
	}
	if string(resp.Data) != "test" {
		t.Errorf("expected 'test', got '%s'", resp.Data)
	}
}

func TestFSMResponse_WithError(t *testing.T) {
	resp := FSMResponse{
		Error: ErrInvalidCommand,
		Data:  nil,
	}

	if resp.Error != ErrInvalidCommand {
		t.Errorf("expected ErrInvalidCommand, got %v", resp.Error)
	}
}

func TestMember(t *testing.T) {
	member := Member{ID: "test-id", Voter: true}

	if member.ID != "test-id" {
		t.Errorf("expected 'test-id', got '%s'", member.ID)
	}
	if !member.Voter {
		t.Error("expected a voter")
	}
	for state, name := range map[State]string{Follower: "Follower", Candidate: "Candidate", Leader: "Leader", Shutdown: "Shutdown", State(9): "Unknown"} {
		if state.String() != name {
			t.Errorf("state %d reads %q, not %q", state, state.String(), name)
		}
	}
}
