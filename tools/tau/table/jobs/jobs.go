// Package jobs renders build jobs for tau-cli.
//
// IT ASKS NOTHING OF ANY SERVICE. Its predecessor called auth once per row to
// turn a numeric repository id into a name — so listing twenty builds was
// twenty round trips, and a project whose repository had been renamed rendered
// as an error. A v2 job carries `repository_key`, which is the name, so a row
// is a function of the job and the local time zone.
package jobs

import (
	"sort"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"

	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
)

// Icon is a status as one character, so a listing scans vertically.
func Icon(s buildv1.JobStatus) string {
	switch s {
	case buildv1.JobStatus_JobStatusOpen:
		return "…"
	case buildv1.JobStatus_JobStatusClaimed:
		return "▸"
	case buildv1.JobStatus_JobStatusSuccess:
		return "✔"
	case buildv1.JobStatus_JobStatusFailed:
		return "✖"
	case buildv1.JobStatus_JobStatusCancelled:
		return "⊘"
	}
	return "?"
}

// Name is a status without the enum's prefix, which is proto's shape and not a
// person's.
func Name(s buildv1.JobStatus) string {
	return strings.TrimPrefix(s.String(), "JobStatus")
}

// Branch is the ref as somebody wrote it. A ref that is not a branch is shown
// whole rather than mangled into looking like one.
func Branch(ref string) string {
	if branch, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
		return branch
	}
	return ref
}

// Short is a commit as the eight characters people actually quote.
func Short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// List renders jobs newest first.
func List(js []*buildv1.Job, zone *time.Location) table.Writer {
	if zone == nil {
		zone = time.Local
	}

	ordered := append([]*buildv1.Job(nil), js...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].GetTimestamp() > ordered[j].GetTimestamp()
	})

	t := table.NewWriter()
	t.AppendHeader(table.Row{"", "when", "repository", "branch", "commit", "job"})
	for _, job := range ordered {
		when := time.Unix(job.GetTimestamp(), 0).In(zone)
		t.AppendRow(table.Row{
			Icon(job.GetStatus()),
			when.Format("01/02/06") + "\n" + when.Format("3:04 PM"),
			job.GetRepositoryKey(),
			Branch(job.GetRef()),
			Short(job.GetCommitSha()),
			job.GetId(),
		})
	}
	t.SetStyle(table.StyleLight)
	return t
}

// One renders a single job as a field list, which is what a person reading one
// build wants: the whole of it, down the page.
func One(job *buildv1.Job, zone *time.Location) table.Writer {
	if zone == nil {
		zone = time.Local
	}

	t := table.NewWriter()
	t.AppendRow(table.Row{"job", job.GetId()})
	t.AppendRow(table.Row{"status", Icon(job.GetStatus()) + " " + Name(job.GetStatus())})
	t.AppendRow(table.Row{"repository", job.GetRepositoryKey()})
	t.AppendRow(table.Row{"branch", Branch(job.GetRef())})
	t.AppendRow(table.Row{"commit", job.GetCommitSha()})
	t.AppendRow(table.Row{"when", time.Unix(job.GetTimestamp(), 0).In(zone).Format(time.RFC1123)})
	if job.GetAttempt() > 1 {
		// SHOWN ONLY WHEN IT IS NOT ONE, because "attempt: 1" on every build
		// teaches a reader to stop seeing the line that matters on the rare one.
		t.AppendRow(table.Row{"attempt", job.GetAttempt()})
	}
	for _, built := range sorted(job.GetAssetCid()) {
		t.AppendRow(table.Row{"built " + built.key, built.value})
	}
	t.SetStyle(table.StyleLight)
	return t
}

type pair struct{ key, value string }

// sorted is a map in a stable order, because a table whose rows move between
// two runs of the same command is one nobody can diff.
func sorted(m map[string]string) []pair {
	out := make([]pair, 0, len(m))
	for k, v := range m {
		out = append(out, pair{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}
