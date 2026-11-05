//go:build !unix

package livelog

import "testing"

func mkfifo(t *testing.T, _ string) bool {
	t.Helper()
	t.Log("no named pipes on this platform")
	return false
}
