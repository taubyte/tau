package session

import "testing"

func TestCloudNameIsSelectedCloud(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Set().CustomCloudUrl("blackhole"); err != nil {
		t.Fatal(err)
	}
	if got := CloudName(); got != "blackhole" {
		t.Fatalf("got %q", got)
	}
}
