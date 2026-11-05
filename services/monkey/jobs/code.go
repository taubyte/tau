package jobs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/pbnjay/memory"
	"github.com/taubyte/tau/core/builders"
	build "github.com/taubyte/tau/pkg/builder"
	"github.com/taubyte/tau/pkg/git"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"
)

func (c code) handle() error {
	if err := c.checkConfig(); err != nil {
		return fmt.Errorf("checking config repo for project failed with: %w", err)
	}

	project, err := projectSchema.Open(projectSchema.SystemFS(c.ConfigRepoRoot))
	if err != nil {
		return fmt.Errorf("opening project from path `%s` failed with: %w", c.ConfigRepoRoot, err)
	}

	// Decompile and get includes and id of each function, website and library
	ops, err := buildTodoFromConfig(project)
	if err != nil {
		return fmt.Errorf("building todo from config for project `%s` failed with: %w", project.Get().Id(), err)
	}

	if err = c.handleOps(ops); err != nil {
		return err
	}

	return nil
}

// handleOps builds the ops, several at a time on a development node, then
// publishes them in order. Each build logs to its own buffer, copied to the job
// log in op order, so concurrent builds do not interleave their output.
func (c code) handleOps(ops []Op) error {
	if len(ops) == 0 {
		return nil
	}

	type built struct {
		log bytes.Buffer
		cid string
		err error
	}
	results := make([]built, len(ops))

	slots := c.buildSlots()
	var wg sync.WaitGroup
	for i, op := range ops {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := &results[i]
			r.cid, r.err = c.buildAndStash(op, &r.log, slots)
		}()
	}
	wg.Wait()

	for i, op := range ops {
		r := &results[i]
		c.LogFile.Write(r.log.Bytes())

		if r.err == nil {
			if r.err = c.publishAsset(op.id, r.cid); r.err != nil {
				r.err = fmt.Errorf("handling build details failed with: %s", r.err)
			}
		}

		if r.err != nil {
			fmt.Fprintf(c.LogFile, "Error building %s: %s\n", op.name, r.err.Error())
			return r.err
		}
	}

	return nil
}

// buildAndStash builds op once a build slot is free and stashes the module.
// On a development node it first looks for a module already built from the
// same sources, by this job or an earlier one, and waits for one being built
// right now rather than building it a second time alongside.
func (c code) buildAndStash(op Op, logs io.Writer, slots chan struct{}) (string, error) {
	compile := func() (string, error) {
		slots <- struct{}{}
		defer func() { <-slots }()

		module, err := c.buildOp(op, logs)
		if err != nil {
			return "", err
		}
		defer module.Close()

		cid, err := c.StashBuildFile(module)
		if err != nil {
			return "", fmt.Errorf("handling build details failed with: %s", err)
		}
		return cid, nil
	}

	if !c.Monkey.Dev() {
		return compile()
	}

	source, err := hashDir(c.opSourcePath(op))
	if err != nil {
		return "", err
	}
	for {
		if cid, ok := c.previousBuild(source); ok {
			fmt.Fprintf(logs, "Building %s ----- unchanged since %s was built, reusing it\n", op.name, cid)
			return cid, nil
		}

		done := make(chan struct{})
		if other, busy := building.LoadOrStore(source, done); busy {
			select {
			case <-other.(chan struct{}):
				continue
			case <-c.ctx.Done():
				return "", c.ctx.Err()
			}
		}

		cid, err := compile()
		if err == nil {
			builtModules.Store(source, cid)
		}
		building.Delete(source)
		close(done)
		return cid, err
	}
}

// building maps the content hash of sources being built right now to a
// channel closed when that build ends.
var building sync.Map

// builtModules maps the content hash of a function's sources to the CID of
// the module built from them, so a development node rebuilds only the
// functions a push changed. A build reads nothing but its own directory (the
// image and its settings are in .taubyte/config.yaml), so the same sources
// build the same module.
var builtModules sync.Map

// reusePrevious publishes, on a development node, the build of a website or
// library repository whose sources are unchanged since an earlier job, and
// reports whether it did. Otherwise it keys the coming build for the next job.
// A push of one repository re-pushes the project's others; most are unchanged.
func (c *Context) reusePrevious(kind string) (bool, error) {
	if !c.Monkey.Dev() {
		return false, nil
	}
	source, err := hashDir(c.WorkDir)
	if err != nil {
		return false, err
	}
	key := kind + ":" + source
	cid, ok := c.previousBuild(key)
	if !ok {
		c.reuseKey = key
		return false, nil
	}

	id, err := c.getResourceRepositoryId()
	if err != nil {
		return true, fmt.Errorf("resource id for %s repo failed with: %w", kind, err)
	}
	fmt.Fprintf(c.LogFile, "Building %s ----- unchanged since %s was built, reusing it\n", kind, cid)
	return true, c.publishAsset(id, cid)
}

// previousBuild returns the module already built from sources with this
// hash, if the node still has it.
func (c Context) previousBuild(source string) (string, bool) {
	cid, ok := builtModules.Load(source)
	if !ok {
		return "", false
	}
	f, err := c.Node.GetFile(c.ctx, cid.(string))
	if err != nil {
		builtModules.Delete(source)
		return "", false
	}
	f.Close()
	return cid.(string), true
}

// devBuildSlots bounds the builds a development node runs at once, across all
// of its jobs: half the cores, so the machine stays usable, and a GiB of
// memory each, since a TinyGo build peaks around 400MiB and running out of
// memory stalls the whole machine (a laptop's WSL froze at 14 builds on 7GiB).
var devBuildSlots = make(chan struct{}, devBuildSlotCount(runtime.NumCPU(), memory.TotalMemory()))

func devBuildSlotCount(cpus int, mem uint64) int {
	byMemory := int(mem >> 30)
	if mem == 0 {
		byMemory = 2
	}
	return min(max(min(cpus/2, byMemory), 1), 8)
}

// buildSlots is shared by every job on a development node. Off one, a job
// builds one op at a time, as it always has: a build node's capacity is
// planned around that.
func (c Context) buildSlots() chan struct{} {
	if c.Monkey.Dev() {
		return devBuildSlots
	}
	return make(chan struct{}, 1)
}

func (c Context) opSourcePath(op Op) string {
	return path.Join(c.gitDir, op.application, op.pathVariable, op.name)
}

// hashDir hashes every path, mode and file under root, skipping .git.
func hashDir(root string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%o\x00", filepath.ToSlash(rel), info.Mode())
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			io.WriteString(h, target)
		case info.Mode().IsRegular():
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(h, f)
			f.Close()
			if err != nil {
				return err
			}
		}
		h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hashing sources in `%s` failed with: %w", root, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (c Context) HandleOp(op Op) (io.ReadSeekCloser, error) {
	return c.buildOp(op, c.LogFile)
}

func (c Context) buildOp(op Op, logs io.Writer) (io.ReadSeekCloser, error) {
	sourcePath := c.opSourcePath(op)
	builder, err := build.New(c.ctx, logs, sourcePath, build.Dev(c.Monkey.Dev()))
	if err != nil {
		err = fmt.Errorf("creating new wasm builder failed with: %w", err)
		return nil, err
	}

	var asset builders.Output
	defer func() {
		fmt.Fprintf(logs, "Building %s -----\n", op.name)
		builder.Close()
	}()

	if asset, err = builder.Build(); err != nil {
		return nil, fmt.Errorf("building function %s/%s failed with: %w", op.application, op.name, err)
	}

	compressedAsset, err := asset.Compress(builders.WASM)
	if err != nil {
		return nil, fmt.Errorf("compressing build files failed with: %w", err)
	}

	return compressedAsset, nil
}

func (c *code) checkConfig() error {
	if len(c.ConfigRepoRoot) < 1 {
		url, err := c.fetchConfigSshUrl()
		if err != nil {
			return fmt.Errorf("failed fetch config ssh url with: %s", err)
		}

		json.NewEncoder(c.LogFile).Encode(struct {
			Op        string `json:"op"`
			Url       string `json:"url"`
			Branch    string `json:"branch"`
			Timestamp int64  `json:"timestamp"`
		}{
			Op:        "git-clone",
			Url:       url,
			Branch:    c.Job.Meta.Repository.Branch,
			Timestamp: time.Now().UnixNano(),
		})
		configRepo, err := git.New(
			c.ctx,
			git.URL(url),
			git.SSHKey(c.ConfigPrivateKey),
			git.Temporary(),
			git.Shallow(),
			git.Branch(c.Job.Meta.Repository.Branch),
			git.Output(c.LogFile),
		)
		if err != nil {
			json.NewEncoder(c.LogFile).Encode(struct {
				Op        string `json:"op"`
				Status    string `json:"status"`
				Timestamp int64  `json:"timestamp"`
				Error     string `json:"error"`
			}{
				Op:        "git-clone",
				Status:    "error",
				Error:     err.Error(),
				Timestamp: time.Now().UnixNano(),
			})
			return fmt.Errorf("getting git repo from url `%s` failed with: %s", url, err)
		}

		c.ConfigRepoRoot = configRepo.Root()

		json.NewEncoder(c.LogFile).Encode(struct {
			Op        string `json:"op"`
			Status    string `json:"status"`
			Timestamp int64  `json:"timestamp"`
		}{
			Op:        "git-clone",
			Status:    "success",
			Timestamp: time.Now().UnixNano(),
		})
	}

	return nil
}
