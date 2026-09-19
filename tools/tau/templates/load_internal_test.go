package templates

import (
	"os"
	"path"
	"testing"
)

// A TEMPLATE REPOSITORY THAT CANNOT BE LOADED IS AN ERROR, NOT A PANIC.
//
// Loading pulls a repository over the network, and `pulling from repo failed
// with EOF` used to take the process down — so a developer's `tau` died with a
// stack trace where it should have said it could not reach the templates. It
// also killed the test binary mid-suite, which is how one flaky link turned
// into a package with no result at all.
func TestALoadFailureIsAnErrorRatherThanAPanic(t *testing.T) {
	wasFolder, wasRepo, wasTemplates := templateFolder, templateRepositoryFolder, _templates
	t.Cleanup(func() {
		templateFolder, templateRepositoryFolder, _templates = wasFolder, wasRepo, wasTemplates
	})
	_templates = nil

	// A FILE WHERE THE CACHE DIRECTORY GOES. Nothing can be created under it,
	// which is what an unwritable or already-taken path looks like.
	blocked := path.Join(t.TempDir(), "taken")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	templateFolder = path.Join(blocked, "templates")
	templateRepositoryFolder = path.Join(templateFolder, "tb_templates")

	got := Get()
	if got == nil {
		t.Fatal("Get answered nothing at all")
	}
	if got.loadErr == nil {
		t.Fatal("a load that could not happen reported no error")
	}

	// AND IT IS NOT REMEMBERED: caching the failure would turn one bad moment
	// into a process that can never see the templates again.
	if _templates != nil {
		t.Error("the failed load was cached")
	}

	// Every entry point answers with it rather than with an empty map.
	if _, err := got.Websites(); err == nil {
		t.Error("Websites answered as though the templates had loaded")
	}
	if _, err := got.Function("go"); err == nil {
		t.Error("Function answered as though the templates had loaded")
	}
}
