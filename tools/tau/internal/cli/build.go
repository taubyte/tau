package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/build/artifact"
	"github.com/taubyte/tau/tools/tau/internal/api"
	"github.com/taubyte/tau/tools/tau/internal/config"
	"github.com/taubyte/tau/tools/tau/internal/project"
)

// build.go is `tau build`: the project built on this machine, by the build a
// cloud runs.
//
// THE DECISION IS THE CLOUD'S OWN. The project is compiled here the way every
// other command compiles it, and the compiled deployments go through the same
// plan and the same recipes a cloud's build reads them through
// (artifact.Plan, artifact.Recipe), into the same engine on this machine's
// container daemon. So what this builds is what a push would build, named by
// the same recipe digest a job log prints.
//
// AGAINST A CLOUD IT PUBLISHES NOTHING. A development cloud that says it takes
// uploads (a dream universe) is handed the tree and what was built from it,
// and deploys it as it would a push; any other cloud is told nothing.

// OutDir is where a build's products land, per environment, relative to the
// project directory.
const OutDir = ".tau/out"

// SecretEnvPrefix is how a build-time secret is handed to `tau build`: the
// secret's name, upper-cased with anything that is not a letter or digit as an
// underscore, after this prefix.
const SecretEnvPrefix = "TAU_SECRET_"

// built is one product of a local build, as recipes.json records it.
type built struct {
	Environment string `json:"environment"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	ID          string `json:"id"`
	Recipe      string `json:"recipe"`
	File        string `json:"file"`
}

type buildOutput struct {
	Project string   `json:"project"`
	Branch  string   `json:"branch"`
	Built   []built  `json:"built"`
	Skipped []string `json:"skipped,omitempty"`
	Pushed  string   `json:"job,omitempty"`
	Status  string   `json:"status,omitempty"`
}

func buildCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build [dir]",
		Short: "build the project on this machine, as a cloud would; a development cloud is handed the result",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := flagString(cmd, "dir")
			if len(args) == 1 {
				dir = args[0]
			}
			return runBuild(cmd, env, dir)
		},
	}
	addDir(cmd)
	addEnv(cmd)
	addProject(cmd)
	cmd.Flags().Bool("clean", false, "build what HEAD holds (git archive HEAD) rather than the working tree")
	cmd.Flags().Bool("published", false, "build against the configuration the cloud has published, rather than this directory's")
	cmd.Flags().String("branch", "", "the branch whose deployments are built (default: the checked-out one)")
	return cmd
}

func runBuild(cmd *cobra.Command, env Env, dir string) error {
	ctx := cmd.Context()

	tree, files, release, err := sourceOf(ctx, dir, flagBool(cmd, "clean"))
	if err != nil {
		return err
	}
	defer release()

	p, err := project.Open(tree)
	if err != nil {
		return err
	}
	if !p.IsProject() {
		return errors.New("no project here: run `tau init` first, or pass a directory")
	}
	branch := flagString(cmd, "branch")
	if branch == "" {
		branch = branchOf(ctx, dir)
	}

	environments, err := environmentsFor(cmd, p, branch)
	if err != nil {
		return err
	}

	published := flagBool(cmd, "published")
	var cl *api.Client
	if published {
		if cl, err = client(cmd, env); err != nil {
			return err
		}
	}

	deployments := make([]artifact.Deployment, 0, len(environments))
	for _, environment := range environments {
		compiled, err := compiledFor(cmd, env, cl, p, branch, environment)
		if err != nil {
			return err
		}
		loaded, err := specs.Load(compiled)
		if err != nil {
			return fmt.Errorf("reading %s's compiled configuration: %w", environment, err)
		}
		deployments = append(deployments, artifact.Deployment{Environment: environment, Project: loaded})
	}

	// THIS DIRECTORY BUILDS WHAT A BARE PATH NAMES. A resource whose source is
	// another repository is that repository's to build.
	var skipped []string
	items, err := artifact.Plan(deployments, func(res specs.Buildable) (bool, error) {
		if res.Source.Git != "" {
			skipped = append(skipped, fmt.Sprintf("%s/%s is built from %s", res.Kind, res.Name, res.Source.Git))
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return err
	}

	// THE BUILD RUNS IN A COPY, as a cloud's runs in a fresh clone: a build
	// writes into its checkout, and the developer's own tree is not where
	// that goes.
	work, err := os.MkdirTemp("", "tau-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := copyFiles(tree, work, files); err != nil {
		return err
	}

	secrets := environSecrets{}
	for _, d := range deployments {
		secrets[d.Environment] = d.Project
	}
	recipes := make([]artifact.Spec, len(items))
	for i, item := range items {
		if recipes[i], err = artifact.Recipe(ctx, secrets, p.ID(), item, work); err != nil {
			return err
		}
	}

	engine := env.Engine
	if engine == nil {
		if engine, err = artifact.NewEngine(nil, artifact.OpenEgress()); err != nil {
			return err
		}
	}

	out := buildOutput{Project: p.ID(), Branch: branch, Built: []built{}, Skipped: skipped}
	products := map[string]string{}
	log := env.Stderr
	if jsonOut(cmd) {
		log = io.Discard
	}
	for _, environment := range environments {
		if err := os.RemoveAll(filepath.Join(dir, OutDir, environment)); err != nil {
			return err
		}
	}
	for i, item := range items {
		digest := recipes[i].Digest()
		fmt.Fprintf(log, "building %s/%s for %s (recipe %s)\n", item.Kind, item.Name, item.Environment, digest)

		file := filepath.Join(OutDir, item.Environment, item.Kind, item.Name)
		if err := make1(ctx, engine, recipes[i], log, filepath.Join(dir, file)); err != nil {
			return fmt.Errorf("building %s/%s for %s: %w", item.Kind, item.Name, item.Environment, err)
		}
		products[digest] = filepath.Join(dir, file)
		out.Built = append(out.Built, built{Environment: item.Environment, Kind: item.Kind, Name: item.Name, ID: item.ID, Recipe: digest, File: file})
	}
	if err := writeRecipes(dir, environments, out.Built); err != nil {
		return err
	}

	if !published {
		if err := pushBuild(cmd, env, dir, tree, files, branch, products, &out); err != nil {
			return err
		}
	}

	if err := emit(cmd, env, out, renderBuild(env, out)); err != nil {
		return err
	}
	if out.Status == "failed" || out.Status == "cancelled" {
		return &exitError{code: 1, msg: "the universe's build of job " + out.Pushed + " " + out.Status}
	}
	return nil
}

// environmentsFor is --env, which must be one the project declares, or every
// deployment the branch feeds.
func environmentsFor(cmd *cobra.Command, p *project.Project, branch string) ([]string, error) {
	if named := flagString(cmd, "env"); named != "" {
		for _, declared := range p.Environments() {
			if declared == named {
				return []string{named}, nil
			}
		}
		return nil, fmt.Errorf("the project declares no environment %q (it declares %s)", named, strings.Join(p.Environments(), ", "))
	}
	fed := p.Deployments(branch)
	if len(fed) == 0 {
		return nil, fmt.Errorf("%q feeds no deployment of this project; pass --env or --branch", branch)
	}
	return fed, nil
}

// compiledFor is one deployment's compiled object: this directory's, refused
// by name when it would not deploy, or the cloud's published one.
func compiledFor(cmd *cobra.Command, env Env, cl *api.Client, p *project.Project, branch, environment string) ([]byte, error) {
	if cl != nil {
		id, err := buildProjectID(cmd, p)
		if err != nil {
			return nil, err
		}
		return cl.Published(cmd.Context(), id, environment)
	}

	// A CLOUD WOULD REFUSE IT, so it is refused here, by the same compile and
	// the same audit, before anything is built.
	if r := p.Validate(cmd.Context(), environment); r.Broken() {
		fmt.Fprint(env.Stderr, theme(env).Report(r))
		return nil, &exitError{code: ExitBroken, msg: environment + " does not deploy as written"}
	}
	return p.Compiled(cmd.Context(), branch, environment)
}

func buildProjectID(cmd *cobra.Command, p *project.Project) (string, error) {
	if id := flagString(cmd, "project"); id != "" {
		return id, nil
	}
	return p.ID(), nil
}

// make1 runs one recipe and writes what it produced to file.
func make1(ctx context.Context, engine artifact.Engine, spec artifact.Spec, log io.Writer, file string) error {
	product, err := engine.Make(ctx, spec, log)
	if err != nil {
		return err
	}
	defer product.Close()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, product); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// writeRecipes records, per environment, what was built and from which recipe.
func writeRecipes(dir string, environments []string, all []built) error {
	for _, environment := range environments {
		mine := []built{}
		for _, b := range all {
			if b.Environment == environment {
				mine = append(mine, b)
			}
		}
		data, err := json.MarshalIndent(mine, "", "  ")
		if err != nil {
			return err
		}
		to := filepath.Join(dir, OutDir, environment, "recipes.json")
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(to, append(data, '\n'), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// pushBuild hands the build to a development cloud that takes one, and
// follows the deploy it queues. A cloud that takes none is told nothing; no
// cloud configured is a build that stays on this machine.
func pushBuild(cmd *cobra.Command, env Env, dir, tree string, files []string, branch string, products map[string]string, out *buildOutput) error {
	cl, err := client(cmd, env)
	if err != nil {
		// NO CLOUD IS A BUILD THAT STAYS ON THIS MACHINE, which is most of
		// what a build is for.
		return nil
	}
	cloud, err := cl.Cloud(cmd.Context())
	if err != nil {
		fmt.Fprintf(env.Stderr, "built here only; %s could not be asked whether it takes a build: %v\n", cl.Base(), err)
		return nil
	}
	if !cloud.Uploads {
		return nil
	}
	id, err := projectIDOf(cmd, dir, out.Project)
	if err != nil {
		return err
	}

	var bundle bytes.Buffer
	if err := artifact.PackBundle(&bundle, tree, files, products); err != nil {
		return err
	}
	job, err := cl.Upload(cmd.Context(), id, "refs/heads/"+branch, &bundle)
	if err != nil {
		return err
	}
	out.Pushed = job

	log := env.Stderr
	if jsonOut(cmd) {
		log = io.Discard
	}
	fmt.Fprintf(log, "%s takes uploads: deploying it as job %s\n", cl.Base(), job)
	final, err := awaitJob(cmd, cl, job, log)
	if err != nil {
		return err
	}
	out.Status = jobStatus(final)
	return nil
}

// projectIDOf is --project, the directory's link, or the project's own id.
func projectIDOf(cmd *cobra.Command, dir, own string) (string, error) {
	if id := flagString(cmd, "project"); id != "" {
		return id, nil
	}
	l, err := config.ReadLink(dir)
	if errors.Is(err, config.ErrNotLinked) {
		return own, nil
	}
	if err != nil {
		return "", err
	}
	return l.ProjectID, nil
}

func renderBuild(env Env, out buildOutput) string {
	t := theme(env)
	var b strings.Builder
	for _, s := range out.Skipped {
		b.WriteString(t.Muted.Render("not built here: "+s) + "\n")
	}
	if len(out.Built) == 0 {
		b.WriteString(t.Done("nothing in " + out.Branch + "'s deployments is built from this directory"))
	}
	rows := make([][]string, 0, len(out.Built))
	for _, x := range out.Built {
		rows = append(rows, []string{x.Environment, x.Kind + "/" + x.Name, x.Recipe[:12], x.File})
	}
	if len(rows) > 0 {
		b.WriteString(t.Table([]string{"ENVIRONMENT", "RESOURCE", "RECIPE", "FILE"}, rows))
	}
	if out.Pushed != "" {
		b.WriteString(fmt.Sprintf("job %s: %s\n", out.Pushed, out.Status))
	}
	return b.String()
}

// environSecrets resolves a build-time secret from this machine's environment:
// the secret the variable names, by its name in the deployment, after
// SecretEnvPrefix. One that is not set is refused by the name to set.
type environSecrets map[string]*specs.Project

func (e environSecrets) Secret(_ context.Context, _, environment, secretID string) (string, error) {
	label := secretID
	if p := e[environment]; p != nil {
		if _, _, name, ok := p.Locate(secretID); ok {
			label = name
		}
	}
	name := SecretEnvPrefix + envName(label)
	value, ok := os.LookupEnv(name)
	if !ok {
		return "", fmt.Errorf("set %s to build with it here", name)
	}
	return value, nil
}

func envName(secret string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return '_'
	}, secret)
}

// branchOf is the checked-out branch, or main outside a repository.
func branchOf(ctx context.Context, dir string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "symbolic-ref", "--short", "-q", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return "main"
	}
	return strings.TrimSpace(string(out))
}

// sourceOf is the tree a build reads and the files in it: HEAD's, for a clean
// build, and otherwise the working tree as git would ship it — tracked files and
// untracked ones it does not ignore — or every file, outside a repository.
// Nothing under .tau is part of it.
func sourceOf(ctx context.Context, dir string, clean bool) (string, []string, func(), error) {
	none := func() {}
	if clean {
		tree, err := os.MkdirTemp("", "tau-clean-")
		if err != nil {
			return "", nil, none, err
		}
		release := func() { os.RemoveAll(tree) }
		files, err := archiveHead(ctx, dir, tree)
		if err != nil {
			release()
			return "", nil, none, err
		}
		return tree, files, release, nil
	}

	out, err := exec.CommandContext(ctx, "git", "-C", dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		files, err := walkFiles(dir)
		return dir, files, none, err
	}
	var files []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" || underTau(name) {
			continue
		}
		if info, err := os.Lstat(filepath.Join(dir, name)); err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, name)
	}
	sort.Strings(files)
	return dir, files, none, nil
}

func underTau(name string) bool {
	return name == ".tau" || strings.HasPrefix(filepath.ToSlash(name), ".tau/")
}

func walkFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	return files, err
}

// archiveHead extracts `git archive HEAD` of dir into tree.
func archiveHead(ctx context.Context, dir, tree string) ([]string, error) {
	var errs bytes.Buffer
	c := exec.CommandContext(ctx, "git", "-C", dir, "archive", "--format=tar", "HEAD")
	c.Stderr = &errs
	stream, err := c.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := c.Start(); err != nil {
		return nil, err
	}

	var files []string
	archive := tar.NewReader(stream)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			c.Wait()
			return nil, fmt.Errorf("--clean reads HEAD: %s", strings.TrimSpace(errs.String()))
		}
		name := path.Clean(header.Name)
		if header.Typeflag != tar.TypeReg || underTau(name) || strings.HasPrefix(name, "../") {
			continue
		}
		to := filepath.Join(tree, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			c.Wait()
			return nil, err
		}
		f, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode).Perm()|0o600)
		if err != nil {
			c.Wait()
			return nil, err
		}
		_, err = io.Copy(f, archive)
		f.Close()
		if err != nil {
			c.Wait()
			return nil, err
		}
		files = append(files, filepath.FromSlash(name))
	}
	if err := c.Wait(); err != nil {
		return nil, fmt.Errorf("--clean reads HEAD: %s", strings.TrimSpace(errs.String()))
	}
	sort.Strings(files)
	return files, nil
}

// copyFiles copies files of from into to, keeping their modes.
func copyFiles(from, to string, files []string) error {
	for _, name := range files {
		src := filepath.Join(from, name)
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			in.Close()
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}
