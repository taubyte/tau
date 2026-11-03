package schema

// A package is what a workload runs and the only kind that builds. One package is
// one artifact: `type` says what the artifact is, `source` where its code is, and
// `build` how it is made. A function, worker, application or sandbox names a
// package and says nothing about any of the three.
//
//	type: assets                     type: oci
//	source: .                        source: api
//	output: web/dist                 variables:
//	build:                               MODE: {value: production}
//	    inputs:                      build:
//	        ui: {name: ui}               dockerfile:
//	    steps:                               path: Dockerfile
//	        - name: site
//	          image: node:22
//	          cache: [~/.pnpm-store]
//	          commands:
//	              - pnpm install --frozen-lockfile
//	              - pnpm --filter web build
//
// No `build:` is a package somebody else built (`source: {image: …}`) or one whose
// source is shipped as it is.

// The types a package may be. Each is also the name of the tier that runs it.
const (
	wasmPackage   = "wasm"
	warpPackage   = "warp"
	ociPackage    = "oci"
	assetsPackage = "assets"
)

var packageTypes = []string{wasmPackage, warpPackage, ociPackage, assetsPackage}

// How an assets package's files are served, by what an unmatched path returns.
const (
	staticServing = "static"
	spaServing    = "spa"
)

// PackageTypes is every type a package may be.
func PackageTypes() []string { return append([]string(nil), packageTypes...) }

// The two ways a build is made, a switch since one's fields mean nothing under the
// other.
const (
	stepsBuild      = "steps"
	dockerfileBuild = "dockerfile"
)

func packageAttributes() []*Attribute {
	return append(contentSource("source", "git", "image", "builtin"),
		String("type", InSet(packageTypes...), Required(), InSection("source"),
			Doc("Type", "What the artifact is. wasm: a component-model component. warp: a JS bundle. oci: a container image. assets: a directory of files, served as they are.")),

		// IsRepoLocation because omitted and `.` differ: omitted is the source as it is,
		// `.` the whole of it once built.
		String("output", IsRepoLocation(), PerEnv(), InSection("build"),
			Doc("Output", "Where the build leaves the artifact, relative to the source, e.g. \"dist\" or \"target/release/app.wasm\". A Dockerfile build's artifact is its image, so it has none.")),

		// Globs, not one path: a monorepo app is rebuilt by a change to what it imports
		// as well as to itself.
		StringSlice("watch", InSection("build"),
			Doc("Watch", "Repository paths whose change rebuilds this package, as globs, e.g. \"web/**\". Omitted means the package's own source.")),
	)
}

var secBuild = Section("build", "Build", "How the source becomes the artifact.")

// buildBlock is how a package's source becomes its artifact. Every field is
// delegable: a repository named with `source: {git: …, config: …}` may carry its
// own build, the way a CI file lives with the code it builds.
func buildBlock() *Node {
	return DefineBlock("build", []*Attribute{
		// The list and the block are the branches, so naming neither or both is the
		// switch's to refuse.
		String("kind", Path(Either(stepsBuild, dockerfileBuild)), Captures(), Delegable(), InSection("build"),
			Doc("Build", "steps: commands run in toolchain images, in order, over the source. dockerfile: a Dockerfile, whose image is the artifact.")),
		// Accessor because the git source's `path` shares the last segment, and a generated
		// client names a field by it.
		String(Key("dockerfile", "path"), IsRepoPath(), Default("Dockerfile"), DefaultWhen("kind", dockerfileBuild), Delegable(), InSection("build"),
			Accessor("Dockerfile"),
			ShowWhen("kind", dockerfileBuild),
			Doc("Dockerfile", "The Dockerfile, relative to the source, which is also its context.")),
		String(Key("dockerfile", "target"), IsVariableName(), Delegable(), InSection("build"),
			ShowWhen("kind", dockerfileBuild),
			Doc("Stage", "Which stage of a multi-stage Dockerfile is the image. Omitted means the last one.")),
	},
		DefineList(stepsBuild, []*Attribute{
			String("name", IsVariableName(), Required(), Delegable(), InSection("build"),
				Doc("Step", "What this step is called, in the build's log and as the owner of its cache.")),
			// Required on every step: otherwise the build changes when the platform's
			// default toolchain does.
			String("image", Required(), Delegable(), InSection("build"),
				Doc("Toolchain", "The OCI image this step runs in, e.g. \"node:22\". The source is mounted into it.")),
			StringSlice("cache", Delegable(), InSection("build"),
				Doc("Cache", "Directories kept for this step's next build, e.g. \"~/.pnpm-store\". Disposable: a build never depends on finding one.")),
			StringSlice("commands", Required(), Delegable(), InSection("build"),
				Doc("Commands", "Run in one shell, in order; the first to fail fails the build. A secret is read with the helper, e.g. \"export NPM_TOKEN=$(/.taubyte/secret npm-token)\".")),
		}, GroupDoc("One step: commands run in one toolchain image, over the source the steps before it left.")),
		DefineGroup("inputs", DefineIter([]*Attribute{
			String("namespace", IsNamespacePath(), Delegable(), InSection("build"),
				Doc("Namespace", "Where that package lives. Omitted means this resource's own scope, then its parents.")),
			String("name", IsVariableName(), Ref("package"), Required(), Delegable(), InSection("build"),
				Doc("Package", "A package whose artifact this build reads, mounted read-only at .inputs/<this entry's key>. It is built first, and a new build of it builds this one again.")),
		}, GroupDoc("Another package's artifact, read by this build."))),
	)
}

// packageRef is what a workload runs: a package, by reference.
func packageRef() []*Attribute {
	return []*Attribute{
		String(Key("package", "namespace"), IsNamespacePath(), InSection("code"),
			Doc("Package Namespace", "Where that package lives. Omitted means this resource's own scope, then its parents.")),
		String(Key("package", "name"), IsVariableName(), Ref("package"), Required(), InSection("code"),
			Doc("Package", "What this runs. The package says what it is, where its code comes from and how it is built.")),
	}
}
