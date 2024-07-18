package git

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestBranch(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	defer func(ctxC context.CancelFunc) {
		ctxC()
		// Give time for repo to Delete.
		time.Sleep(50 * time.Millisecond)
	}(ctxC)
	_, err := New(
		ctx,
		URL(fmt.Sprintf("https://github.com/%s/%s.git", testRepoUser, testRepoName)),
		Token(testRepoToken(t)),
		Author(testRepoUser, ""),
		Branch("dreamland"),
		Temporary(),
	)
	if err != nil {
		t.Errorf("Testing New failed with error: %s", err.Error())
		return
	}
}
