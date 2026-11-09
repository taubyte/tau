package dream

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	builderv2 "github.com/taubyte/tau/pkg/proto/gen/tau/builder/v2"
	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/services/plm/run"
	workerHosted "github.com/taubyte/tau/services/worker/hosted"
)

// recorded is plm's records as a node reads them: what it was told was built.
type recorded map[string]string

func (r recorded) Artifact(_ context.Context, project, environment, _, resource string) (string, error) {
	cid, ok := r[builtKey(project, environment, resource)]
	if !ok {
		return "", workerHosted.ErrNotBuilt
	}
	return cid, nil
}

func TestANodeLooksUpWhatTheCloudBuiltThenPLMsRecords(t *testing.T) {
	kept := &builtHere{}
	lookup := &artifacts{kept: kept}

	_, err := lookup.Artifact(t.Context(), "QmP", "prod", "package", "QmR")
	require.ErrorIs(t, err, workerHosted.ErrNotBuilt, "a node not started yet reads nothing")

	kept.keep("QmP", "prod", []*builderv2.Stored{{ResourceId: "QmR", Cid: "bafy-here"}})
	cid, err := lookup.Artifact(t.Context(), "QmP", "prod", "package", "QmR")
	require.NoError(t, err)
	require.Equal(t, "bafy-here", cid, "what the cloud built itself")

	lookup.records = recorded{builtKey("QmP", "prod", "QmOther"): "bafy-recorded"}
	cid, err = lookup.Artifact(t.Context(), "QmP", "prod", "package", "QmOther")
	require.NoError(t, err)
	require.Equal(t, "bafy-recorded", cid, "and anything else is plm's records'")
}

// unsealable is a builder whose seal is refused.
type unsealable struct{ run.Builder }

func (unsealable) Seal(context.Context, *builderv2.Sealing) (*builderv2.Sealed, error) {
	return nil, errors.New("the log went")
}

func TestASealThatFailsCostsTheLog(t *testing.T) {
	d := &direct{runs: unsealable{}}
	require.Empty(t, d.seal(t.Context(), run.Work{JobID: "j", Attempt: 1}, plmv2.JobStatus_JobStatusSuccess).GetLog())
}

func TestAPauseEndsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, pause(ctx), context.Canceled)
	require.NoError(t, pause(t.Context()))
}

func TestAJobIDNamesOnePushAndOneEnvironment(t *testing.T) {
	p := Push{Repository: "github.com/acme/app", Ref: "refs/heads/main", Commit: "c"}
	require.Equal(t, jobID(p, ""), jobID(p, ""))
	require.NotEqual(t, jobID(p, ""), jobID(p, "prod"), "a plan is not its environment's build")
	require.NotEqual(t, jobID(Push{Repository: "github.com/acme/ap", Ref: "prefs/heads/main", Commit: "c"}, ""), jobID(p, ""),
		"the fields are framed, so moving an edge between them is another id")
}

func TestAPlanEndsAsPLMWouldRecordIt(t *testing.T) {
	p := Push{Repository: "github.com/acme/app", Ref: "refs/heads/main", Commit: "c"}
	w := run.Work{JobID: jobID(p, ""), RepositoryKey: p.Repository, Ref: p.Ref, CommitSHA: p.Commit, Attempt: 1}

	failed := planOf(p, w, &builderv2.Planned{Error: "no project here", Said: "cloned\n"})
	require.Equal(t, plmv2.JobStatus_JobStatusFailed, failed.GetStatus())
	require.Equal(t, "cloned\nerror: no project here", failed.GetReason(), "what it said, then why it failed")

	none := planOf(p, w, &builderv2.Planned{})
	require.Equal(t, plmv2.JobStatus_JobStatusFailed, none.GetStatus())
	require.Equal(t, "the commit feeds no environment", none.GetReason())

	fed := planOf(p, w, &builderv2.Planned{Environments: []string{"prod", "preview"}})
	require.Equal(t, plmv2.JobStatus_JobStatusSuccess, fed.GetStatus())
	require.Equal(t, []string{jobID(p, "prod"), jobID(p, "preview")}, fed.GetChildren())
	require.Equal(t, p.Repository, fed.GetRepositoryKey())
}
