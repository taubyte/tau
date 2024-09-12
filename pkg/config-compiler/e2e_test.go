package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/pkg/config-compiler/compile"
	"github.com/taubyte/tau/pkg/config-compiler/decompile"
	"github.com/taubyte/tau/pkg/config-compiler/fixtures"
	"github.com/taubyte/tau/pkg/git"
	projectLib "github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/utils/maps"
	"gotest.tools/v3/assert"
)

var fakeMeta = patrick.Meta{
	Repository: patrick.Repository{
		Provider: "github",
		Branch:   "master",
		ID:       12356,
	},
	HeadCommit: patrick.HeadCommit{
		ID: "345690",
	},
}

const configRepo = "https://github.com/taubyte-test/tb_testproject"

var generatedDomainRegExp = regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)

func TestCompile(t *testing.T) {
	project, err := fixtures.Project()
	if err != nil {
		t.Error(err)
		return
	}

	rc, err := compile.CompilerConfig(project, fakeMeta, generatedDomainRegExp)
	if err != nil {
		t.Error(err)
		return
	}

	compiler, err := compile.New(rc, compile.Dev())
	if err != nil {
		t.Error(err)
		return
	}

	err = compiler.Build()
	if err != nil {
		t.Error(err)
		return
	}

	jsonBytes, err := json.Marshal(compiler.Object())
	if err != nil {
		t.Error(err)
		return
	}

	osFS := afero.NewOsFs()
	// Uncomment the below to refresh the file if changes to the yaml written in internal
	// err = afero.WriteFile(osFS, "./compile_test2.json", jsonBytes, 0644)
	// if err != nil {
	// 	t.Error(err)
	// 	return
	// }

	expectedBytes, err := afero.ReadFile(osFS, "./compile_test.json")
	if err != nil {
		t.Error(err)
		return
	}

	if string(expectedBytes) != string(jsonBytes) {
		t.Error("Bytes don't match")
		return
	}

}

func TestFromCloneCompile(t *testing.T) {
	testCtx, testCtxC := context.WithCancel(context.Background())
	defer func() {
		s := (3 * time.Second)
		go func() {
			time.Sleep(s)
			testCtxC()
		}()
		time.Sleep(s)
	}()

	gitRoot := "./testGIT"
	defer os.RemoveAll(gitRoot)
	gitRootConfig := gitRoot + "/config"
	os.MkdirAll(gitRootConfig, 0755)

	// clone repo
	err := cloneConfig(testCtx, configRepo, gitRootConfig)
	assert.NilError(t, err)

	// read with seer
	project, err := projectLib.Open(projectLib.SystemFS(gitRootConfig))
	if err != nil {
		t.Error(err)
		return
	}

	rc, err := compile.CompilerConfig(project, fakeMeta, generatedDomainRegExp)
	if err != nil {
		t.Error(err)
		return
	}

	compiler, err := compile.New(rc, compile.Dev())
	if err != nil {
		t.Error(err)
		return
	}

	err = compiler.Build()
	if err != nil {
		t.Error(err)
		return
	}

	maps.Display("", compiler.Object())
}

func TestNoTNS(t *testing.T) {
	testDir := "./testGIT/test1"
	testDir2 := "./testGIT/test2"
	defer os.RemoveAll("./testGIT")

	ctx, ctxC := context.WithTimeout(context.Background(), 10*time.Second)
	defer ctxC()

	os.MkdirAll(testDir, 0755)
	os.MkdirAll(testDir2, 0755)

	err := cloneConfig(ctx, configRepo, testDir)
	assert.NilError(t, err)

	// read with seer
	projectIface, err := projectLib.Open(projectLib.SystemFS(testDir))
	if err != nil {
		t.Error(err)
		return
	}

	rc, err := compile.CompilerConfig(projectIface, fakeMeta, generatedDomainRegExp)
	if err != nil {
		t.Error(err)
		return
	}

	compiler, err := compile.New(rc, compile.Dev())
	if err != nil {
		t.Error(err)
		return
	}

	defer compiler.Close()

	err = compiler.Build()
	if err != nil {
		t.Error(err)
		return
	}

	_map := compiler.Object()
	decompiler, err := decompile.New(afero.NewBasePathFs(afero.NewOsFs(), testDir2), _map)
	if err != nil {
		t.Error(err)
		return
	}

	decompiledIface, err := decompiler.Build()
	if err != nil {
		t.Error(err)
		return
	}

	rc, err = compile.CompilerConfig(decompiledIface, fakeMeta, generatedDomainRegExp)
	if err != nil {
		t.Error(err)
		return
	}

	compiler2, err := compile.New(rc, compile.Dev())
	if err != nil {
		t.Error(err)
		return
	}
	defer compiler2.Close()

	err = compiler2.Build()
	if err != nil {
		t.Error(err)
		return
	}

	_map2 := compiler2.Object()
	if !reflect.DeepEqual(_map, _map2) {
		t.Error("Objects not equal")

		b1, err := json.Marshal(_map)
		if err != nil {
			t.Error(err)
			return
		}
		b2, err := json.Marshal(_map2)
		if err != nil {
			t.Error(err)
			return
		}

		fmt.Println("\n\nB1:\n", string(b1))
		fmt.Println("\n\nB2:\n", string(b2))
		return
	}
}

func cloneConfig(ctx context.Context, url, root string) error {
	if _, err := git.New(ctx, git.URL(url), git.Root(root)); err != nil {
		return err
	}

	return nil
}
