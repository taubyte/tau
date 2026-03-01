package projectLib_test

import (
	"testing"

	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"gotest.tools/v3/assert"
)

func TestCleanGitURL(t *testing.T) {
	tests := []struct {
		name   string
		apiURL string
		want   string
	}{
		{"removes /repos", "https://api.example.com/repos/foo/bar", "https://example.com/foo/bar"},
		{"removes api. prefix", "https://api.github.com/user/repo", "https://github.com/user/repo"},
		{"both", "https://api.gitlab.com/repos/group/proj", "https://gitlab.com/group/proj"},
		{"unchanged when no match", "https://git.example.com/org/repo", "https://git.example.com/org/repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := projectLib.CleanGitURL(tt.apiURL)
			assert.Equal(t, got, tt.want)
		})
	}
}
