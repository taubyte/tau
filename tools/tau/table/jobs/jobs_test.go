package jobs

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
)

func job(id string, status buildv1.JobStatus, at int64) *buildv1.Job {
	return &buildv1.Job{
		Id:            id,
		Status:        status,
		RepositoryKey: "github.com/acme/shop",
		Ref:           "refs/heads/main",
		CommitSha:     "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c",
		Timestamp:     at,
	}
}

func TestEveryStatusHasAnIconAndAName(t *testing.T) {
	// A STATUS THIS TABLE HAS NOT HEARD OF MUST NOT RENDER AS ANOTHER ONE. The
	// enum grows in build.proto and this file is where that shows up, so the
	// whole declared set is walked rather than the four anyone thinks of.
	for value, name := range buildv1.JobStatus_name {
		status := buildv1.JobStatus(value)
		require.NotEqual(t, "?", Icon(status), name)
		require.Equal(t, strings.TrimPrefix(name, "JobStatus"), Name(status))
	}

	// And one that is not in the enum at all is visibly unknown rather than
	// borrowing the zero value's look, which is "Open" — a finished build
	// showing as still queued.
	require.Equal(t, "?", Icon(buildv1.JobStatus(99)))
}

func TestARefIsShownAsSomebodyWroteIt(t *testing.T) {
	require.Equal(t, "main", Branch("refs/heads/main"))
	require.Equal(t, "release/1.4", Branch("refs/heads/release/1.4"))

	// A ref that is not a branch is shown WHOLE rather than mangled into
	// looking like one: "v1.0.0" and the tag refs/tags/v1.0.0 are different
	// things and a reader has to be able to tell.
	require.Equal(t, "refs/tags/v1.0.0", Branch("refs/tags/v1.0.0"))
	require.Equal(t, "", Branch(""))
}

func TestACommitIsTheEightCharactersPeopleQuote(t *testing.T) {
	require.Equal(t, "0f1e2d3c", Short("0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c"))
	require.Equal(t, "abc", Short("abc"), "a short one is not padded or truncated into a lie")
	require.Equal(t, "", Short(""))
}

// NEWEST FIRST, because the build somebody is asking about is almost always the
// one that just ran.
func TestListIsNewestFirst(t *testing.T) {
	rendered := List([]*buildv1.Job{
		job("older", buildv1.JobStatus_JobStatusSuccess, 1_000),
		job("newest", buildv1.JobStatus_JobStatusFailed, 3_000),
		job("middle", buildv1.JobStatus_JobStatusCancelled, 2_000),
	}, time.UTC).Render()

	require.Less(t, strings.Index(rendered, "newest"), strings.Index(rendered, "middle"))
	require.Less(t, strings.Index(rendered, "middle"), strings.Index(rendered, "older"))

	// The repository is IN the job, so a row costs no round trip — its
	// predecessor asked auth once per row to turn a numeric id into a name.
	require.Contains(t, rendered, "github.com/acme/shop")
	require.Contains(t, rendered, "main")
	require.Contains(t, rendered, "0f1e2d3c")
}

func TestListOfNothing(t *testing.T) {
	rendered := List(nil, time.UTC).Render()
	require.Contains(t, rendered, "REPOSITORY", "the header still says what would have been there")
}

func TestOne(t *testing.T) {
	j := job("j1", buildv1.JobStatus_JobStatusSuccess, 1_700_000_000)
	j.AssetCid = map[string]string{"web": "bafyweb", "api": "bafyapi"}

	rendered := One(j, time.UTC).Render()
	require.Contains(t, rendered, "j1")
	require.Contains(t, rendered, "Success")
	require.Contains(t, rendered, "github.com/acme/shop")
	// The whole sha, because this is the view somebody copies it out of.
	require.Contains(t, rendered, j.GetCommitSha())

	// A MAP RENDERS IN A STABLE ORDER, or the same command twice cannot be
	// diffed.
	require.Less(t, strings.Index(rendered, "built api"), strings.Index(rendered, "built web"))

	// ATTEMPT IS SHOWN ONLY WHEN IT IS NOT ONE: "attempt: 1" on every build
	// teaches a reader to stop seeing the line that matters on the rare one.
	require.NotContains(t, rendered, "attempt")

	j.Attempt = 3
	require.Contains(t, One(j, time.UTC).Render(), "attempt")
}

// A nil zone is the reader's own, which is what somebody looking at a build
// wants — and it must not be a nil dereference.
func TestTheLocalZoneIsTheDefault(t *testing.T) {
	require.NotPanics(t, func() {
		List([]*buildv1.Job{job("j1", buildv1.JobStatus_JobStatusOpen, 1_700_000_000)}, nil).Render()
		One(job("j1", buildv1.JobStatus_JobStatusOpen, 1_700_000_000), nil).Render()
	})
}
