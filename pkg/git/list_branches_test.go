package git

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/exp/slices"
)

func openTestRepository(t *testing.T, ops ...Option) (repo *Repository, deferment func(), err error) {
	ctx, ctxC := context.WithCancel(context.Background())
	deferment = func() {
		ctxC()
		// Give time for repo to Delete.
		time.Sleep(150 * time.Millisecond)
	}

	repo, err = New(ctx, append([]Option{
		URL(fmt.Sprintf("https://github.com/%s/%s.git", testRepoUser, testRepoName)),
		Token(testRepoToken(t)),
		Author(testRepoUser, testRepoEmail),
		Temporary(),

		// Append ops
	}, ops...)...)
	if err != nil {
		err = fmt.Errorf("opening basic repository failed with: %s", err)
	}

	return
}

func TestListBranches(t *testing.T) {
	repo, deferment, err := openTestRepository(t)
	if err != nil {
		t.Error(err)
		return
	}
	defer deferment()

	branches, fetchErr, err := repo.ListBranches(true)
	if err != nil {
		t.Error(err)
		return
	}
	if fetchErr != nil && !strings.Contains(fetchErr.Error(), "already up-to-date") {
		t.Error(fetchErr)
		return
	}

	if len(branches) < 2 {
		t.Errorf("expected at least 2 branches, got %d", len(branches))
		return
	}

	testBranch := "dreamland"
	if !slices.Contains(branches, testBranch) {
		t.Errorf("expected branch `%s` to be in list of branches", testBranch)
		return
	}

	err = repo.Checkout(testBranch)
	if err != nil {
		t.Error(err)
		return
	}
}
