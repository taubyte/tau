package containers_test

//go:generate tar cvf fixtures/docker.tar -C fixtures/docker/ .

import (
	"bytes"
	"context"
	"os"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	ci "github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/pkg/containers/gc"
)

// Test Path Variables
const (
	FixturesDir   = "fixtures"
	DockerDir     = "fixtures/docker"
	DockerFile    = "Dockerfile"
	DockerTarBall = "docker.tar"
	TestScript    = "helloWorld.sh"
	TestVarScript = "envVars.sh"
)

// Test Variables
var (
	message           = "testing message"
	basicCommand      = []string{"echo", message}
	testScriptCommand = []string{"/bin/sh", "/src/" + TestScript}
	TestScriptMessage = "HELLO WORLD"
	testVarsCommand   = []string{"/bin/sh", "/src/" + TestVarScript, "$" + testEnv}
	testEnv           = "TEST"
	testVal           = "Value42"
	testCustomImage   = "taubyte/test:test2"
	testVolume        = "volume"
)

var (
	VolumePath        string
	DockerDirPath     string
	DockerFilePath    string
	DockerTarBallPath string
	ScriptPath        string
	VarScriptPath     string
)

func init() {
	if wd, err := os.Getwd(); err != nil {
		panic("Getting working directory failed with: " + err.Error())
	} else {
		FixturesPath := path.Join(wd, FixturesDir)
		VolumePath = path.Join(FixturesPath, testVolume)
		DockerDirPath = path.Join(FixturesDir, DockerDir)
		DockerFilePath = path.Join(DockerDirPath, DockerFile)
		DockerTarBallPath = path.Join(FixturesPath, DockerTarBall)
		ScriptPath = path.Join(VolumePath, TestScript)
		VarScriptPath = path.Join(VolumePath, TestVarScript)
	}
}

func TestContainerBasicCommand(t *testing.T) {
	ci.ForceRebuild = true

	ctx := context.Background()

	cli, err := ci.New()
	if err != nil {
		t.Error(err)
		return
	}

	file, err := os.OpenFile(DockerTarBallPath, os.O_RDWR, 0444)
	if err != nil {
		t.Errorf("Opening docker tarball failed with: %s", err)
		return
	}

	defer file.Close()

	image, err := cli.Image(ctx, testCustomImage, ci.Build(file))
	if err != nil {
		t.Error(err)
		return
	}

	container, err := image.Instantiate(
		ctx,
		ci.Command(basicCommand),
	)
	if err != nil {
		t.Error(err)
		return
	}

	logs, err := container.Run(ctx)
	if err != nil {
		t.Error(err)
		return
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(logs.Combined())
	out := buf.String()
	if !strings.Contains(out, message) {
		t.Error("Container output not the same as the given message")
		return
	}

	err = logs.Close()
	if err != nil {
		t.Error(err)
		return
	}
}

func TestContainerCleanUpInterval(t *testing.T) {
	ci.ForceRebuild = true

	ctx := context.Background()
	cli, err := ci.New()
	if err != nil {
		t.Error(err)
		return
	}

	err = gc.Start(ctx, gc.Interval(20*time.Second), gc.MaxAge(10*time.Second))
	if err != nil {
		t.Error(err)
		return
	}

	file, err := os.OpenFile(DockerTarBallPath, os.O_RDWR, 0444)
	if err != nil {
		t.Error(err)
		return
	}
	defer file.Close()

	image, err := cli.Image(ctx, testCustomImage, ci.Build(file))
	if err != nil {
		t.Error(err)
		return
	}

	container, err := image.Instantiate(ctx, ci.Command(basicCommand))
	if err != nil {
		t.Error(err)
		return
	}

	logs, err := container.Run(ctx)
	if err != nil {
		t.Error(err)
		return
	}

	buf := new(bytes.Buffer)
	buf.ReadFrom(logs.Combined())
	out := buf.String()
	if !strings.Contains(out, message) {
		t.Error("Container output not the same as the given message")
		return
	}

	err = logs.Close()
	if err != nil {
		t.Error(err)
		return
	}

	// Image was just built; verify it exists locally.
	img, err := cli.Image(ctx, testCustomImage)
	if err != nil {
		if img == nil || !img.Exists(ctx) {
			t.Errorf("Failed to get image: %v", err)
			return
		}
	}
	if img == nil || !img.Exists(ctx) {
		t.Errorf("Expected to find docker image %s", testCustomImage)
		return
	}

	time.Sleep(20 * time.Second)

	// After cleanup interval, image should be gone.
	img2, err2 := cli.Image(ctx, testCustomImage)
	if err2 == nil && img2 != nil && img2.Exists(ctx) {
		t.Error("Expected to find no containers after clean interval")
	}
}

func TestContainerMount(t *testing.T) {
	ci.ForceRebuild = true

	ctx := context.Background()
	cli, err := ci.New()
	if err != nil {
		t.Error(err)
		return
	}

	file, err := os.OpenFile(DockerTarBallPath, os.O_RDWR, 0444)
	if err != nil {
		t.Error(err)
		return
	}
	defer file.Close()

	image, err := cli.Image(ctx, testCustomImage, ci.Build(file))
	if err != nil {
		t.Error(err)
		return
	}

	container, err := image.Instantiate(
		ctx,
		ci.Volume(VolumePath, "/src"),
		ci.Command(testScriptCommand),
	)
	if err != nil {
		t.Error(err)
		return
	}
	logs, err := container.Run(ctx)
	if err != nil {
		t.Error(err)
		return
	}

	buf := new(bytes.Buffer)
	buf.ReadFrom(logs.Combined())
	out := buf.String()
	if !strings.Contains(out, TestScriptMessage) {
		t.Error("Container output not the same as the given message")
		return
	}

	err = logs.Close()
	if err != nil {
		t.Error(err)
		return
	}
}

func TestContainerBasicVariables(t *testing.T) {
	ci.ForceRebuild = true

	ctx := context.Background()
	cli, err := ci.New()
	if err != nil {
		t.Error(err)
		return
	}

	file, err := os.OpenFile(DockerTarBallPath, os.O_RDWR, 0444)
	if err != nil {
		t.Error(err)
		return
	}
	defer file.Close()

	image, err := cli.Image(ctx, testCustomImage, ci.Build(file))
	if err != nil {
		t.Error(err)
		return
	}

	vars := map[string]string{testEnv: testVal}
	container, err := image.Instantiate(
		ctx,
		ci.Command(testVarsCommand),
		ci.Volume(VolumePath, "/src"),
		ci.Variables(vars),
	)
	if err != nil {
		t.Error(err)
		return
	}

	logs, err := container.Run(ctx)
	if err != nil {
		t.Error(err)
		return
	}

	buf := new(bytes.Buffer)
	buf.ReadFrom(logs.Combined())
	out := buf.String()
	if !strings.Contains(out, testVal) {
		t.Error("Container output not the same as the given message")
		return
	}

	err = logs.Close()
	if err != nil {
		t.Error(err)
		return
	}

}

func TestContainerParallel(t *testing.T) {
	var (
		wg    sync.WaitGroup
		count int = 4
	)

	wg.Add(count)

	for i := 0; i < count; i++ {
		go func() {
			TestContainerMount(t)
			wg.Done()
		}()
	}

	wg.Wait()
}
