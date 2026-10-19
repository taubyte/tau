package query

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// Table is rows as headers and cells, the one shape both the plain-text
// output and the TUI's tables are drawn from.
type Table struct {
	Headers []string
	Rows    [][]string
}

// Short is a peer id cut to something a column can hold.
func Short(peer string) string {
	if len(peer) <= 12 {
		return peer
	}
	return peer[:4] + "…" + peer[len(peer)-6:]
}

func yes(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func list(s []string) string {
	if len(s) == 0 {
		return "-"
	}
	return strings.Join(s, ",")
}

// NodesTable is the fleet as a table.
func NodesTable(ns []Node) Table {
	t := Table{Headers: []string{"PEER", "READY", "LOAD", "AGE", "SERVING", "ADDRESSES"}}
	for _, n := range ns {
		t.Rows = append(t.Rows, []string{
			Short(n.Peer), yes(n.Ready), strconv.FormatFloat(n.Load, 'f', 2, 64),
			n.Age.String(), list(n.Serving), list(n.Address),
		})
	}
	return t
}

// ServicesTable is the services as a table.
func ServicesTable(ss []Service) Table {
	t := Table{Headers: []string{"SERVICE", "NODES", "READY", "PEERS"}}
	for _, s := range ss {
		peers := make([]string, len(s.Nodes))
		for i, p := range s.Nodes {
			peers[i] = Short(p)
		}
		t.Rows = append(t.Rows, []string{s.Name, strconv.Itoa(len(s.Nodes)), strconv.Itoa(s.Ready), list(peers)})
	}
	return t
}

// ProjectsTable is the projects as a table.
func ProjectsTable(ps []Project) Table {
	t := Table{Headers: []string{"ID", "CONFIG", "ENVIRONMENTS"}}
	for _, p := range ps {
		t.Rows = append(t.Rows, []string{p.ID, p.Config, list(p.Environments)})
	}
	return t
}

// JobsTable is the jobs as a table.
func JobsTable(js []Job) Table {
	t := Table{Headers: []string{"ID", "PROJECT", "STATUS", "REPOSITORY", "REF", "COMMIT", "ATTEMPT", "TIME"}}
	for _, j := range js {
		commit := j.Commit
		if len(commit) > 8 {
			commit = commit[:8]
		}
		t.Rows = append(t.Rows, []string{
			j.ID, j.Project, j.Status, j.Repository, j.Ref, commit,
			strconv.Itoa(int(j.Attempt)), j.Time.Format(time.RFC3339),
		})
	}
	return t
}

// Write prints v as JSON when format is "json", or t as aligned text when it
// is "table" or "".
func Write(w io.Writer, format string, t Table, v any) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	case "", "table":
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, strings.Join(t.Headers, "\t"))
		for _, r := range t.Rows {
			fmt.Fprintln(tw, strings.Join(r, "\t"))
		}
		return tw.Flush()
	}
	return fmt.Errorf("unknown output %q: want table or json", format)
}
