package cspbft

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// census_test.go is the pkg/placement idiom: tests that reflect over a type or
// parse this package's own source and fail when somebody adds a field or a
// dependency the design says must not exist. They are cheap, and they are the
// enforcement — the alternative is a comment nobody re-reads.

func fieldNames(v any) []string {
	tp := reflect.TypeOf(v)
	out := make([]string, 0, tp.NumField())
	for i := 0; i < tp.NumField(); i++ {
		out = append(out, tp.Field(i).Name)
	}
	sort.Strings(out)
	return out
}

func TestNoProtocolMessageCarriesAClock(t *testing.T) {
	tp := reflect.TypeOf(Message{})
	for i := 0; i < tp.NumField(); i++ {
		fl := tp.Field(i)
		if fl.Type == reflect.TypeOf(time.Time{}) || fl.Type == reflect.TypeOf(time.Duration(0)) {
			t.Fatalf("Message.%s is a clock: a replica that can assert when something happened can advance "+
				"another replica's deadlines by lying about the time", fl.Name)
		}
		for _, bad := range []string{"Time", "Clock", "Deadline", "Timestamp", "Expiry", "At"} {
			if fl.Name == bad {
				t.Fatalf("Message.%s reads as a clock", fl.Name)
			}
		}
	}
	want := []string{
		"Checkpoints", "Client", "Digest", "Epoch", "From", "Kind", "PrePrepares",
		"Prepared", "Proposal", "RotationDigest", "Seq", "Serial", "Signature", "View", "ViewChanges",
	}
	if got := fieldNames(Message{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("Message fields are %v, want %v — Serial is a per-client counter and is the one field that looks "+
			"like a timestamp and is not", got, want)
	}
}

func TestASnapshotCarriesNothingAReplicaCouldLieItselfInWith(t *testing.T) {
	want := []string{"Epoch", "Replicas", "StateDigest"}
	if got := fieldNames(Snapshot{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("Snapshot fields are %v, want %v.\n\n"+
			"A latency, a timeout count or an uptime here would be pkg/stats' self-reported Region at the worst "+
			"possible stakes: the primary is the one replica that can cheaply censor, reorder and stall, so it is "+
			"the position an attacker actually wants. StateDigest is the only agreed datum a scorer gets, and "+
			"everything a scorer reads must be derivable from it.", got, want)
	}
}

// parsePackage reads this package's own source, comments discarded, so that a
// mention in a doc comment does not count as a dependency.
func parsePackage(t *testing.T) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p, ok := pkgs["cspbft"]
	if !ok {
		t.Fatal("this package did not parse as cspbft")
	}
	return fset, p.Files
}

func TestTheCertificateFileNamesNoScorer(t *testing.T) {
	_, files := parsePackage(t)
	for _, name := range []string{"certificate.go", "log.go"} {
		f, ok := files[name]
		if !ok {
			t.Fatalf("%s is gone: the file that counts votes is supposed to exist", name)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			for _, banned := range []string{"Scorer", "Snapshot", "rotation", "RoundRobin"} {
				if id.Name == banned {
					t.Fatalf("%s names %s.\n\nCounting votes is the one place a reputation system would be "+
						"catastrophic rather than merely wrong, so the file that counts them must have no path "+
						"to the file that ranks. Weighting a vote has to be a compile error.", name, banned)
				}
			}
			return true
		})
	}
}

func TestTheActionSetIsClosed(t *testing.T) {
	_, files := parsePackage(t)
	var got []string
	for _, f := range files {
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "isAction" || fn.Recv == nil || len(fn.Recv.List) != 1 {
				continue
			}
			if id, ok := fn.Recv.List[0].Type.(*ast.Ident); ok {
				got = append(got, id.Name)
			}
		}
	}
	sort.Strings(got)
	want := []string{
		"Broadcast", "CancelTimer", "Evidence", "Execute", "FetchState",
		"Notice", "Persist", "Send", "SetTimer", "TakeCheckpoint",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the action set is %v, want %v.\n\nAdding one is a deliberate review against the protocol's "+
			"invariants — an action is something a caller is obliged to do, and an eleventh obligation that "+
			"nobody noticed is an obligation nobody honours.", got, want)
	}
}

func TestNothingInThisPackageReadsAClockOrOpensAnything(t *testing.T) {
	_, files := parsePackage(t)
	banned := map[string]string{
		"time.Now":   "a clock read makes a test's result depend on the machine it ran on",
		"os.Open":    "this package has no I/O",
		"go func":    "this package has no concurrency",
		"net.Dial":   "this package has no transport",
		"time.Sleep": "this package has no clock",
	}
	for name, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if _, ok := n.(*ast.GoStmt); ok {
				t.Fatalf("%s starts a goroutine: %s", name, banned["go func"])
			}
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if why, bad := banned[x.Name+"."+sel.Sel.Name]; bad {
				t.Fatalf("%s calls %s.%s: %s", name, x.Name, sel.Sel.Name, why)
			}
			return true
		})
	}
}

func TestTheOnlyImportsAreTheStandardLibrarysSmallestParts(t *testing.T) {
	_, files := parsePackage(t)
	allowed := map[string]bool{
		`"crypto/sha256"`:   true,
		`"encoding/binary"`: true,
		`"errors"`:          true,
		`"fmt"`:             true,
		`"sort"`:            true,
		`"time"`:            true,
	}
	for name, f := range files {
		for _, imp := range f.Imports {
			if !allowed[imp.Path.Value] {
				t.Fatalf("%s imports %s.\n\nA consensus package knows about replicas and messages, not about what "+
					"they are agreeing on: no services, no node, no libp2p, and nothing from this repository.", name, imp.Path.Value)
			}
		}
	}
}
