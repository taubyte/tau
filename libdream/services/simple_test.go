package services

import (
	"testing"
)

func TestClientsWithDefaults(t *testing.T) {
	config := ClientsWithDefaults(ValidClients()...)
	if config.Seer == nil {
		t.Error("Seer is nil")
		return
	}
	if config.Auth == nil {
		t.Error("Auth is nil")
		return
	}
	if config.Patrick == nil {
		t.Error("Patrick is nil")
		return
	}
	if config.TNS == nil {
		t.Error("TNS is nil")
		return
	}
	if config.Monkey == nil {
		t.Error("Monkey is nil")
		return
	}
	if config.Hoarder == nil {
		t.Error("Hoarder is nil")
		return
	}
}
