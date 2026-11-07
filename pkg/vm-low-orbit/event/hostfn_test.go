package event

import (
	"context"
	"testing"

	wazy "github.com/samyfodil/wazy"
)

// go-sdk <= v0.3.9 imports the header list under its old names; a module
// importing a name the host lacks does not load.
func TestOldHeaderListImportsExported(t *testing.T) {
	ctx := context.Background()
	r := wazy.NewRuntime(ctx)
	defer r.Close(ctx)
	b := r.NewHostModuleBuilder("taubyte/sdk")
	(&Factory{}).RegisterHostFunctions(b)
	c, err := b.Compile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := c.ExportedFunctions()
	for _, n := range []string{"getHttpEventHeaderVarNameById", "getHttpEventHeaderVarNameByIdSize", "getHttpEventRequestHeaderKeys"} {
		if _, ok := got[n]; !ok {
			t.Errorf("host does not export %s", n)
		}
	}
}
