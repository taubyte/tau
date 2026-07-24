package repositoryLib

import (
	"context"

	"github.com/taubyte/tau/pkg/git"
)

// GitRepository is the slice of pkg/git the CLI actually drives — clone/open is
// the constructor (NewRepository), the rest are the operations. *git.Repository
// satisfies it; a test double implements it to exercise the clone/push/pull/
// checkout flows without a real remote.
type GitRepository interface {
	Commit(message, files string) error
	Push() error
	Pull() error
	Checkout(branch string) error
	ListBranches(fetch bool) (branches []string, fetchErr error, err error)
	Root() string
}

// NewRepository constructs (clones, or opens if already present) a git repo.
// The single seam through which every CLI git operation goes; tests swap it for
// a fake.
var NewRepository = func(ctx context.Context, opts ...git.Option) (GitRepository, error) {
	return git.New(ctx, opts...)
}
