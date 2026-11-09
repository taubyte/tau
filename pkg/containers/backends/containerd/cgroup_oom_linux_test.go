//go:build linux

package containerd

import "testing"

func TestOOMKillCounted(t *testing.T) {
	for events, want := range map[string]bool{
		"low 0\nhigh 0\nmax 3\noom 1\noom_kill 1\noom_group_kill 0\n": true,
		"low 0\nhigh 0\nmax 3\noom 0\noom_kill 0\n":                   false,
		"": false,
	} {
		if got := oomKillCounted(events); got != want {
			t.Errorf("oomKillCounted(%q) = %v, want %v", events, got, want)
		}
	}
}
