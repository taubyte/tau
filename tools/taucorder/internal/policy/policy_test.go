package policy

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	_ "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	_ "github.com/taubyte/tau/pkg/proto/gen/tau/builder/v2"
	_ "github.com/taubyte/tau/pkg/proto/gen/tau/database/v2"
	_ "github.com/taubyte/tau/pkg/proto/gen/tau/discovery/v2"
	_ "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	_ "github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v2"
	_ "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
	_ "github.com/taubyte/tau/pkg/proto/gen/tau/register/v2"
	_ "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	_ "github.com/taubyte/tau/pkg/proto/gen/tau/sqlsync/v2"
	_ "github.com/taubyte/tau/pkg/proto/gen/tau/worker/v2"
)

func TestTableMatchesDescriptors(t *testing.T) {
	seen := map[string]bool{}
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(fd.Package()), "tau.") {
			return true
		}
		svcs := fd.Services()
		for i := 0; i < svcs.Len(); i++ {
			sd := svcs.Get(i)
			ms := sd.Methods()
			for j := 0; j < ms.Len(); j++ {
				name := string(sd.FullName()) + "/" + string(ms.Get(j).Name())
				seen[name] = true
				if Of(string(sd.FullName()), string(ms.Get(j).Name())) == Unknown {
					t.Errorf("%s is not classified", name)
				}
			}
		}
		return true
	})
	for svc, ms := range Table {
		for m := range ms {
			if !seen[svc+"/"+m] {
				t.Errorf("%s/%s is classified but does not exist", svc, m)
			}
		}
	}
}

func TestParse(t *testing.T) {
	s, m, err := Parse("/tau.register.v2.RegisterService/Resolve")
	if err != nil || s != "tau.register.v2.RegisterService" || m != "Resolve" {
		t.Fatal(s, m, err)
	}
	for _, bad := range []string{"", "/", "/x", "/x/", "//m", "/a/b/c"} {
		if _, _, err := Parse(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestPermits(t *testing.T) {
	type testCase struct {
		a    Allow
		c    Class
		want bool
	}
	cases := []testCase{
		{Allow{}, Read, true},
		{Allow{}, Sensitive, false},
		{Allow{}, Write, false},
		{Allow{}, Unknown, false},
		{Allow{Sensitive: true}, Sensitive, true},
		{Allow{Writes: true}, Write, true},
		{Allow{Writes: true}, Sensitive, false},
		{Allow{Writes: true, Sensitive: true}, Unknown, false},
	}
	for _, c := range cases {
		if got := c.a.Permits(c.c); got != c.want {
			t.Errorf("%+v %v: %v", c.a, c.c, got)
		}
	}
}

func TestClassString(t *testing.T) {
	for c, s := range map[Class]string{Read: "read", Sensitive: "sensitive", Write: "write", Unknown: "unknown"} {
		if c.String() != s {
			t.Error(c, s)
		}
	}
}
