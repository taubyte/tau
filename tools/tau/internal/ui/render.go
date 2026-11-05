// Package ui is how the CLI looks: lipgloss styles and tables for everything it
// prints, huh for what it asks, and bubbletea models for the views that stay
// live (a followed log, a wait). Every renderer takes the writer it renders for,
// so output to a pipe or a file carries no colour and is what the golden tests
// pin.
package ui

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/session"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/tools/tau/internal/project"
)

// Theme is the styles, bound to one output.
type Theme struct {
	Title, Muted, OK, Warn, Bad, Accent, Header lipgloss.Style
}

// NewTheme binds the styles to w: a terminal gets colour, anything else plain.
func NewTheme(w io.Writer) Theme {
	r := lipgloss.NewRenderer(w)
	return Theme{
		Title:  r.NewStyle().Bold(true),
		Muted:  r.NewStyle().Faint(true),
		OK:     r.NewStyle().Foreground(lipgloss.Color("2")),
		Warn:   r.NewStyle().Foreground(lipgloss.Color("3")),
		Bad:    r.NewStyle().Foreground(lipgloss.Color("1")).Bold(true),
		Accent: r.NewStyle().Foreground(lipgloss.Color("6")),
		Header: r.NewStyle().Bold(true).Padding(0, 1),
	}
}

// Table is rows under headers, drawn as every table here is.
func (t Theme) Table(headers []string, rows [][]string) string { return t.table(headers, rows) }

func (t Theme) table(headers []string, rows [][]string) string {
	cell := lipgloss.NewStyle().Padding(0, 1)
	return table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(t.Muted).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return t.Header
			}
			return cell
		}).
		Render() + "\n"
}

// severity orders and labels a finding.
func (t Theme) severity(s string) (int, string) {
	switch s {
	case "broken":
		return 0, t.Bad.Render("BROKEN")
	case "check":
		return 1, t.Warn.Render("CHECK ")
	default:
		return 2, t.Muted.Render("NOTE  ")
	}
}

// Report renders a validation: a compile failure, or the audit's findings worst
// first with a one-line tally.
func (t Theme) Report(r project.Report) string {
	var b strings.Builder
	if r.Error != "" {
		b.WriteString(t.Bad.Render("✗ does not compile") + "\n")
		for _, line := range strings.Split(r.Error, "\n") {
			b.WriteString("  " + line + "\n")
		}
		return b.String()
	}

	findings := append([]session.Note(nil), r.Findings...)
	sort.SliceStable(findings, func(i, j int) bool {
		a, _ := t.severity(findings[i].Severity)
		c, _ := t.severity(findings[j].Severity)
		return a < c
	})
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.Severity]++
		_, label := t.severity(f.Severity)
		where := f.Kind + "/" + f.Name
		if f.Scope != "" {
			where += " in " + f.Scope
		}
		fmt.Fprintf(&b, "%s %s  %s\n", label, t.Accent.Render(where), f.Title)
		if f.Detail != "" {
			for _, line := range strings.Split(f.Detail, "\n") {
				b.WriteString("       " + t.Muted.Render(line) + "\n")
			}
		}
	}
	for _, d := range r.Deferred {
		fmt.Fprintf(&b, "%s %s %v %s\n", t.Muted.Render("LATER "), d.Validator, d.Value, t.Muted.Render("(checked at deploy)"))
	}
	if counts["broken"] > 0 {
		b.WriteString(t.Bad.Render(fmt.Sprintf("✗ %d broken, %d to check, %d noted", counts["broken"], counts["check"], counts["note"])) + "\n")
	} else {
		b.WriteString(t.OK.Render(fmt.Sprintf("✓ compiles — %d to check, %d noted", counts["check"], counts["note"])) + "\n")
	}
	return b.String()
}

// Status is a job status without its enum prefix.
func Status(s plmv2.JobStatus) string {
	return strings.ToLower(strings.TrimPrefix(s.String(), "JobStatus"))
}

func (t Theme) status(s plmv2.JobStatus) string {
	label := Status(s)
	switch s {
	case plmv2.JobStatus_JobStatusSuccess:
		return t.OK.Render(label)
	case plmv2.JobStatus_JobStatusFailed:
		return t.Bad.Render(label)
	case plmv2.JobStatus_JobStatusCancelled:
		return t.Muted.Render(label)
	}
	return t.Warn.Render(label)
}

func when(unix int64) string {
	if unix == 0 {
		return "-"
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// Jobs renders jobs as a table, newest first.
func (t Theme) Jobs(jobs []*plmv2.Job) string {
	if len(jobs) == 0 {
		return t.Muted.Render("no jobs yet — push the project's repositories to start one") + "\n"
	}
	sorted := append([]*plmv2.Job(nil), jobs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].GetTimestamp() > sorted[j].GetTimestamp() })
	rows := make([][]string, 0, len(sorted))
	for _, j := range sorted {
		rows = append(rows, []string{j.GetId(), t.status(j.GetStatus()), j.GetRepositoryKey(), j.GetRef(), short(j.GetCommitSha()), fmt.Sprint(j.GetAttempt()), when(j.GetTimestamp())})
	}
	return t.table([]string{"JOB", "STATUS", "REPOSITORY", "REF", "COMMIT", "TRY", "QUEUED"}, rows)
}

func (t Theme) Job(j *plmv2.Job) string {
	var b strings.Builder
	field := func(k, v string) { fmt.Fprintf(&b, "%s %s\n", t.Muted.Render(fmt.Sprintf("%-11s", k)), v) }
	b.WriteString(t.Title.Render("job "+j.GetId()) + "\n")
	field("status", t.status(j.GetStatus()))
	field("repository", j.GetRepositoryKey())
	field("ref", j.GetRef())
	field("commit", j.GetCommitSha())
	field("attempt", fmt.Sprint(j.GetAttempt()))
	field("queued", when(j.GetTimestamp()))
	for _, k := range sortedKeys(j.GetAssetCid()) {
		field("built", k+" → "+j.GetAssetCid()[k])
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Secrets renders secret names. Values are never shown: the cloud does not give
// them back.
func (t Theme) Secrets(environment string, names []string) string {
	if len(names) == 0 {
		return t.Muted.Render("no secrets set in "+environment) + "\n"
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	var b strings.Builder
	b.WriteString(t.Title.Render(environment) + "\n")
	for _, n := range sorted {
		b.WriteString("  " + n + "\n")
	}
	return b.String()
}

// Views renders where a compiled project goes: one block per destination with
// what each resource is there.
func (t Theme) Views(v session.Viewed) string {
	var b strings.Builder
	for _, view := range v.Views {
		dest := view.Destination
		if dest == "" {
			dest = "everywhere"
		}
		b.WriteString(t.Title.Render(dest) + "\n")
		if len(view.Roles) == 0 {
			b.WriteString("  " + t.Muted.Render(fmt.Sprintf("%d keys, every resource whole", len(view.Object))) + "\n")
			continue
		}
		names := namesIn(view.Object)
		rows := make([][2]string, 0, len(view.Roles))
		for id, role := range view.Roles {
			label := id
			if n, ok := names[id]; ok {
				label = n
			}
			rows = append(rows, [2]string{role, label})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i][0] != rows[j][0] {
				return rows[i][0] < rows[j][0]
			}
			return rows[i][1] < rows[j][1]
		})
		for _, r := range rows {
			fmt.Fprintf(&b, "  %s %s\n", t.Accent.Render(fmt.Sprintf("%-10s", r[0])), r[1])
		}
	}
	return b.String()
}

// namesIn maps each compiled resource id in a view's object to kind/name, so a
// view reads as the resources an author wrote rather than as ids. It reads the
// compiled shape only — kind, then id, then the resource's identity name — and
// a resource it cannot name is shown by id.
func namesIn(object map[string]any) map[string]string {
	out := map[string]string{}
	project, _ := object[interp.ProjectKey].(map[string]any)
	_, nameField := engine.IdentityOf(schema.CompileRoot())
	for kind, v := range project {
		resources, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for id, r := range resources {
			fields, ok := r.(map[string]any)
			if !ok {
				continue
			}
			if name, ok := fields[nameField].(string); ok {
				out[id] = kind + "/" + name
			}
		}
	}
	return out
}

func (t Theme) Kinds(kinds []session.Kind) string {
	var b strings.Builder
	b.WriteString(t.Title.Render("kinds") + "\n")
	for _, k := range kinds {
		if k.Container {
			fmt.Fprintf(&b, "  %s %s\n", k.Name, t.Muted.Render("(a scope: resources go inside it with --in)"))
			continue
		}
		b.WriteString("  " + k.Name + "\n")
	}
	return b.String()
}

// Issues renders what a resource still owes.
func (t Theme) Issues(issues []project.Issue) string {
	var b strings.Builder
	for _, i := range issues {
		fmt.Fprintf(&b, "%s %s  %s\n", t.Warn.Render("•"), strings.Join(i.Field, "."), t.Muted.Render(i.Message))
	}
	return b.String()
}

func (t Theme) Done(msg string) string { return t.OK.Render("✓") + " " + msg + "\n" }
