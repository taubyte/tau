package common

import "testing"

func TestRequiresRaftCluster(t *testing.T) {
	if RequiresRaftCluster(nil) {
		t.Fatal("nil services should not require raft")
	}
	if RequiresRaftCluster([]string{}) {
		t.Fatal("empty services should not require raft")
	}
	if RequiresRaftCluster([]string{Auth, Seer}) {
		t.Fatal("auth+seer should not require raft")
	}
	if !RequiresRaftCluster([]string{Patrick}) {
		t.Fatal("patrick should require raft")
	}
	if !RequiresRaftCluster([]string{Auth, Patrick}) {
		t.Fatal("mixed list with patrick should require raft")
	}
}
