package common

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBuildCommandRunsCRLFAndNonExecutableScripts(t *testing.T) {
	for name, body := range map[string]string{
		"lf":   "#!/bin/bash\necho ok-$X\n",
		"crlf": "#!/bin/bash\r\nX=crlf\r\necho ok-$X\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			script := filepath.Join(t.TempDir(), "build.sh")
			if err := os.WriteFile(script, []byte(body), 0o644); err != nil { // not executable
				t.Fatal(err)
			}
			args := buildCommand(script)
			out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
			want := map[string]string{"lf": "ok-\n", "crlf": "ok-crlf\n"}[name]
			if err != nil || string(out) != want {
				t.Fatalf("got %q, %v; want %q", out, err, want)
			}
		})
	}
}
