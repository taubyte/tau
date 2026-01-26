package raft

import (
	"testing"

	"github.com/hashicorp/raft"
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
	member := Member{
		ID:       "test-id",
		Address:  raft.ServerAddress("127.0.0.1:8080"),
		Suffrage: raft.Voter,
	}

	if member.ID != "test-id" {
		t.Errorf("expected 'test-id', got '%s'", member.ID)
	}
	if member.Address != raft.ServerAddress("127.0.0.1:8080") {
		t.Errorf("expected '127.0.0.1:8080', got '%s'", member.Address)
	}
	if member.Suffrage != raft.Voter {
		t.Errorf("expected Voter, got %v", member.Suffrage)
	}
}

func TestTimeoutPreset_Constants(t *testing.T) {
	if PresetLocal != "local" {
		t.Errorf("expected 'local', got '%s'", PresetLocal)
	}
	if PresetRegional != "regional" {
		t.Errorf("expected 'regional', got '%s'", PresetRegional)
	}
	if PresetGlobal != "global" {
		t.Errorf("expected 'global', got '%s'", PresetGlobal)
	}
}
