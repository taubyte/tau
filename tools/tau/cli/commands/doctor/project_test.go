package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorProjectChecks(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	dom := root + "/p/config/domains/d.yaml"
	os.MkdirAll(filepath.Dir(dom), 0o755)
	os.WriteFile(dom, []byte("fqdn: site.default.localtau\n"), 0o644)
	files := []string{dom, root + "/p/code/A.go", root + "/p/code/a.go", root + "/p/code/" + strings.Repeat("x", 240)}

	got := map[string]result{}
	lookup := func(h string) error { return errors.New("no such host") }
	checkProject(files, lookup, func(s, n, m, f string) { got[n] = result{s, n, m, f} })

	if r := got["localtau"]; r.status != "WARN" || !strings.Contains(r.msg, "site.default.localtau") || !strings.Contains(r.fix, "Host: site.default.localtau") {
		t.Fatalf("localtau: %+v", r)
	}
	if r := got["long paths"]; r.status != "WARN" || !strings.Contains(r.msg, "1 path") {
		t.Fatalf("long paths: %+v", r)
	}
	if r := got["file names"]; r.status != "WARN" || !strings.Contains(r.msg, "A.go") {
		t.Fatalf("file names: %+v", r)
	}

	got = map[string]result{}
	checkProject(files[:2], func(string) error { return nil }, func(s, n, m, f string) { got[n] = result{s, n, m, f} })
	if len(got) != 0 {
		t.Fatalf("clean project warned: %+v", got)
	}
}
