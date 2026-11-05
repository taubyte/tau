package proto

import (
	"bytes"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	pb "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/taubyte/tau/tools/protogen"
)

// The semantic rules for the .proto schema, which buf does not check: buf lint
// reads style, and nine semantic mutations of plm.proto (a numeric repository
// id, a oneof over forge names, the webhook's changed paths crossing the queue,
// RPCs named Clone and RunContainer) all passed buf lint, build and generate.
//
// It walks the compiled FileDescriptorSet (`buf build -o`, then descriptorpb),
// which is what the wire is generated from, over every v2 proto:
//
//	§1  a repository is one string key
//	§2  nothing is named for a v1 service
//	§3  package, directory and protocol id agree
//	§4  no provider webhook payload crosses
//	§5  no build capability is an RPC
//	§6  v2's schema imports only v2's schema
//	§7  no file declares go_package
//
// What it cannot check is listed in the last subtest.
func TestTheRulesBufCannotCheck(t *testing.T) {
	root, err := protogen.FindRepoRoot(mustGetwd(t))
	if err != nil {
		t.Fatal(err)
	}

	s := load(t, root)
	t.Logf("pkg/proto: %d .proto files (%d on disk), %d packages, %d messages, "+
		"%d fields, %d oneofs, %d enums, %d enum values, %d services, %d rpcs",
		len(s.files), s.onDisk, len(s.packages), len(s.messages), len(s.fields),
		len(s.oneofs), len(s.enums), len(s.values), len(s.services), len(s.methods))

	// If buf builds fewer files than are on disk, every rule below is checking a
	// smaller tree than it thinks.
	if s.onDisk == 0 || len(s.files) != s.onDisk {
		t.Fatalf("buf built %d files from a module holding %d .proto sources on disk — "+
			"every rule below would be checking a tree that isn't there", len(s.files), s.onDisk)
	}

	// §1. A repository is one string key
	//
	// The key is a normalized clone URL, `host/owner/repo`, computable offline by
	// both sides. The host is the provider, so GitLab, Gitea and a mirror are values,
	// not branches; a oneof over forge names or a numeric repo id would close that.
	//
	// The rule is about repository identity. auth.proto's `GithubHook` and
	// `int64 provider_hook_id` are correct: a webhook is a resource the forge owns
	// and numbers. What is refused is a forge or an integer standing in for the
	// repository.
	t.Run("ARepositoryIsAStringKeyAndNeverANumberOrAForge", func(t *testing.T) {
		var found []string
		for _, f := range s.fields {
			switch {
			case numeric(f.d) && hasSegment(f.name, "repo", "repository") != "":
				found = append(found, f.where+" is a numeric repository id; "+
					"the key is a string")
			case numeric(f.d) && hasSegment(f.name, forges...) != "":
				found = append(found, f.where+" is a forge's own number standing in for identity")
			}
		}
		for _, o := range s.oneofs {
			if hasSegment(o.name, "repo", "repository", "provider") != "" {
				found = append(found, o.where+" branches on where a repository lives; "+
					"the host in the key already says")
			}
		}
		// "never a `provider` field beside a repository key" is auth.proto's
		// own phrasing and plm.proto restates it: two fields that must
		// always agree are one fact spelled twice, and the second one is the
		// one an attacker gets to choose.
		for _, m := range s.messages {
			if !slices.ContainsFunc(m.d.GetField(), func(f *descriptorpb.FieldDescriptorProto) bool {
				return hasSegment(f.GetName(), "repo", "repository") != "" && hasSegment(f.GetName(), "key") != ""
			}) {
				continue
			}
			for _, f := range m.d.GetField() {
				if f.GetName() == "provider" || hasSegment(f.GetName(), forges...) != "" {
					found = append(found, m.where+"."+f.GetName()+
						" names a forge beside a repository key; the key already carries the host")
				}
			}
		}
		refuse(t, "§1 a repository is named by its clone-URL key alone", found)
	})

	// §2. Nothing is named for a v1 service
	//
	// v2 does not carry the names patrick, monkey, hoarder or substrate. The
	// descriptor's names are checked, not its comments, which may cite v1 code.
	t.Run("NothingIsNamedForAV1Service", func(t *testing.T) {
		var found []string
		for _, n := range s.everything {
			if w := hasSegment(n.name, v1Services...); w != "" {
				found = append(found, n.where+" is named for v1's "+w)
			}
		}
		for _, p := range s.packages {
			if w := hasSegment(p.name, v1Services...); w != "" {
				found = append(found, p.where+" is named for v1's "+w)
			}
		}
		refuse(t, "§2 v2 does not carry v1's service names", found)
	})

	// §3. Package, directory and protocol id agree
	//
	// The protocol id is "/" plus the proto package with dots as slashes, derived
	// (rpc.Protocol) and never typed twice. The "tau." segment keeps ids from
	// colliding with other protocols on the swarm (v1's were bare, /auth/v1).
	//
	// The directory half is buf's PACKAGE_DIRECTORY_MATCH too, but buf.yaml's lint
	// `except:` list is one line from disabling it.
	t.Run("PackageDirectoryAndProtocolIdAgree", func(t *testing.T) {
		var found []string
		for _, f := range s.files {
			pkg := f.GetPackage()
			if !packageShape.MatchString(pkg) {
				found = append(found, f.GetName()+" declares package "+strconv.Quote(pkg)+
					", not tau.<service>.v<N>")
				continue
			}
			if dir := path.Dir(f.GetName()); dir != strings.ReplaceAll(pkg, ".", "/") {
				found = append(found, f.GetName()+" sits at "+dir+" but declares package "+pkg)
			}
		}
		refuse(t, "§3 the protocol id is the package, and it may not be one v1 holds", found)
	})

	// §4. No provider webhook payload crosses
	//
	// The payload's per-commit `added|modified|removed` is provider-shaped,
	// truncates past 20 commits or 3000 files, and would carry attacker-controlled
	// data from the internet-facing listener into the backend's path filter. The
	// backend diffs before..after itself after the clone. `head_commit`,
	// `pushed_at` and the payload's clone URLs go with it.
	//
	// Changed-path names are refused only on a repeated field, so `modified_at`
	// passes.
	t.Run("NoProviderWebhookPayloadCrossesTheWire", func(t *testing.T) {
		var found []string
		for _, f := range s.fields {
			switch {
			// the per-commit path lists, which are lists and nothing else
			case f.d.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED &&
				hasSegment(f.name, "added", "modified", "removed", "changed") != "":
			// the payload's own envelope, and the timestamp v1 folded into a job id
			case hasSegment(f.name, "commits", "payload", "pushed") != "":
			// head_commit, whatever it is suffixed with
			case hasSegment(f.name, "head") != "" && hasSegment(f.name, "commit") != "":
			// the four URLs a provider hands over, all of them derivable from the key
			case hasSegment(f.name, "url") != "" && hasSegment(f.name, "clone", "ssh", "git", "html") != "":
			default:
				continue
			}
			found = append(found, f.where+" is the provider's webhook payload, not the repository's own answer")
		}
		refuse(t, "§4 what a stranger's POST said does not cross the queue", found)
	})

	// §5. No build capability is an RPC
	//
	// Build is split on a trust boundary: the frontend holds no container runtime,
	// clone credentials, build images or publish rights; the backend holds all four
	// and no ingress.
	//
	// This is a proxy: the descriptor does not say which role serves a service. A
	// working copy or a container runtime is exercised in-process by the role that
	// holds the credentials, never requested over RPC, so an RPC named for one is
	// wrong either way. "publish" is not on the list, since the register needs a
	// Publish RPC.
	t.Run("NoBuildCapabilityIsAnRPC", func(t *testing.T) {
		var found []string
		for _, m := range s.methods {
			if w := hasSegment(m.name, "clone", "checkout", "container"); w != "" {
				found = append(found, m.where+" puts "+w+" on the wire; a working copy and a "+
					"container runtime are exercised in-process by the role that holds the "+
					"credentials, never asked for over RPC")
			}
		}
		refuse(t, "§5 a build capability is held, not offered", found)
	})

	// §6. v2's schema imports only v2's schema
	//
	// Cross-service types belong in tau/common/v2/common.proto. buf.yaml's
	// workspace includes tools/spore-drive/proto and ee/proto, so an import across
	// that boundary would build clean; the check is against what this module
	// declares on disk.
	t.Run("V2sSchemaImportsOnlyV2sSchema", func(t *testing.T) {
		var found []string
		for _, f := range s.files {
			for _, dep := range f.GetDependency() {
				if slices.Contains(s.ours, dep) || strings.HasPrefix(dep, "google/protobuf/") {
					continue
				}
				found = append(found, f.GetName()+" imports "+dep+", which this module does not declare")
			}
		}
		refuse(t, "§6 a v2 schema imports v2's own tree and well-known types, nothing else", found)
	})

	// §7. No file declares go_package
	//
	// Managed mode sets it from go_package_prefix (buf.gen.yaml), so a declared
	// one does nothing and reads as if it did.
	t.Run("NoFileDeclaresGoPackage", func(t *testing.T) {
		var found []string
		for _, f := range s.files {
			if opt := f.GetOptions().GetGoPackage(); opt != "" {
				found = append(found, f.GetName()+" declares go_package = "+strconv.Quote(opt)+
					", which managed mode overrides and nothing reads")
			}
		}
		refuse(t, "§7 go_package is buf.gen.yaml's, not the schema's", found)
	})

	// What this file cannot check:
	//
	//  1. Which role serves a service block: a descriptor knows no processes. §5
	//     refuses capability names as the nearest proxy.
	//  2. Which services face the internet: Descriptor.Ingress, tested at the node
	//     registry.
	//  3. Who holds clone credentials: no wire format states it. The import walk
	//     (no ingress, no pkg/http) is a Go test.
	//  4. Who may call an RPC: each service's authz.go, from the
	//     libp2p-authenticated caller.
	//  5. What a string means: `repository_key` is a repokey only by doc comment.
	//  6. `before` and `after`: ruled out of the crossing, but too ordinary to
	//     refuse by name.
	t.Run("WhatThisCannotCheck", func(t *testing.T) {
		for _, gap := range []string{
			"which ROLE serves a service block — §5 refuses capability names as a proxy",
			"which services face the internet — Descriptor.Ingress, tested at the node registry",
			"which process holds clone credentials — not a fact any wire format states",
			"who may call an RPC — authz.go, from the libp2p-authenticated caller",
			"what a string means — `repository_key` is a repokey only in the doc comment",
			"`before` and `after` — ruled out of the crossing, too ordinary to refuse by name",
		} {
			t.Log("NOT CHECKED HERE: " + gap)
		}
	})
}

var (
	// forges are values in a clone URL's host, never branches in a schema.
	forges = []string{"github", "gitlab", "bitbucket", "gitea", "codeberg", "gogs", "forgejo", "sourcehut"}

	v1Services = []string{"patrick", "monkey", "hoarder", "substrate"}

	// packageShape is tau.<service>.v<N>. The version makes a breaking change a new
	// package, protocol id and listener beside the old one.
	packageShape = regexp.MustCompile(`^tau(\.[a-z][a-z0-9]*)+\.v[0-9]+$`)
)

// hasSegment reports which of words the name is built out of, matching whole
// segments rather than substrings: `provider_hook_id` must not read as a
// repository provider, and `commit_sha` must not read as the payload's
// `commits`. It returns the word it matched, so a failure says what it saw.
//
// A descriptor mixes two conventions — fields and oneofs are lower_snake,
// messages, services and RPCs are CamelCase — so segments splits on both.
// Splitting on the underscore alone is not a smaller version of this rule,
// it is a hole: `RunContainer` is one underscore-segment and survived the
// mutation that added it.
func hasSegment(name string, words ...string) string {
	segs := segments(name)
	for _, w := range words {
		if slices.Contains(segs, w) {
			return w
		}
	}
	return ""
}

// segments breaks an identifier at underscores and at case boundaries,
// lowercased. A run of capitals stays one segment until the last of them
// starts a new word: "GetHTTPCache" is get/http/cache.
func segments(name string) []string {
	var (
		out   []string
		cur   strings.Builder
		runes = []rune(name)
	)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}
	for i, r := range runes {
		switch {
		case r == '_' || r == '.':
			flush()
		case unicode.IsUpper(r):
			prevLower := i > 0 && !unicode.IsUpper(runes[i-1])
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if i > 0 && (prevLower || nextLower) {
				flush()
			}
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

func numeric(f *descriptorpb.FieldDescriptorProto) bool {
	switch f.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_INT32, descriptorpb.FieldDescriptorProto_TYPE_INT64,
		descriptorpb.FieldDescriptorProto_TYPE_UINT32, descriptorpb.FieldDescriptorProto_TYPE_UINT64,
		descriptorpb.FieldDescriptorProto_TYPE_SINT32, descriptorpb.FieldDescriptorProto_TYPE_SINT64,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED32, descriptorpb.FieldDescriptorProto_TYPE_FIXED64,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED32, descriptorpb.FieldDescriptorProto_TYPE_SFIXED64,
		descriptorpb.FieldDescriptorProto_TYPE_FLOAT, descriptorpb.FieldDescriptorProto_TYPE_DOUBLE:
		return true
	}
	return false
}

func refuse(t *testing.T, rule string, found []string) {
	t.Helper()
	if len(found) == 0 {
		return
	}
	slices.Sort(found)
	t.Errorf("%s\n  %s", rule, strings.Join(found, "\n  "))
}

type ref struct {
	name  string
	where string
}

type fieldRef struct {
	ref
	d *descriptorpb.FieldDescriptorProto
}

type msgRef struct {
	ref
	d *descriptorpb.DescriptorProto
}

type schema struct {
	files    []*descriptorpb.FileDescriptorProto
	onDisk   int
	packages []ref
	messages []msgRef
	fields   []fieldRef
	oneofs   []ref
	enums    []ref
	values   []ref
	services []ref
	methods  []ref

	// ours is every .proto this module declares, by descriptor name — the
	// set §6 measures an import against.
	ours []string

	// everything is every declared name except the package, which §3 owns
	// and §2 checks separately — one flat list so a name rule cannot forget
	// a kind of declaration.
	everything []ref
}

// load compiles the module into a FileDescriptorSet and flattens it. The
// module is built by buf rather than parsed here for the same reason
// tools/protogen shells out to it: buf resolves the workspace, the imports
// and the well-known types, and a second parser in this repo would be a
// second opinion about what the schema is.
func load(t *testing.T, root string) *schema {
	t.Helper()

	dir := filepath.Join(root, "pkg/proto")
	out := filepath.Join(t.TempDir(), "descriptor.binpb")
	// NOT `exec.Command("buf", ...)`. buf on PATH was an unstated precondition,
	// and this test fails rather than skips when the schema cannot be built —
	// on purpose, since a proto guard that goes quiet is worse than one that is
	// absent. protogen.Buf falls back to the pinned version in the module cache,
	// so "install buf first" stops being a step anybody has to know about.
	cmd := protogen.Buf("build", "-o", out)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("buf build in pkg/proto: %v\n%s", err, stderr.String())
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	set := new(descriptorpb.FileDescriptorSet)
	if err := pb.Unmarshal(raw, set); err != nil {
		t.Fatal(err)
	}

	s := &schema{onDisk: countProtoFiles(t, dir)}
	for _, f := range set.GetFile() {
		// A FileDescriptorSet carries the transitive closure, so the
		// well-known types and — the whole point of §6 — any file reached
		// across the buf workspace arrive here too. Ours are the ones that
		// exist on disk in this module; the rest are what §6 is about.
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f.GetName()))); err != nil {
			continue
		}
		s.ours = append(s.ours, f.GetName())
		s.files = append(s.files, f)
		s.packages = append(s.packages, ref{f.GetPackage(), f.GetName() + ": package " + f.GetPackage()})
		for _, m := range f.GetMessageType() {
			s.message(f.GetName(), "", m)
		}
		for _, e := range f.GetEnumType() {
			s.enum(f.GetName(), "", e)
		}
		for _, svc := range f.GetService() {
			at := f.GetName() + ": " + svc.GetName()
			s.services = append(s.services, ref{svc.GetName(), at})
			for _, m := range svc.GetMethod() {
				s.methods = append(s.methods, ref{m.GetName(), at + "." + m.GetName()})
			}
		}
	}
	s.everything = slices.Concat(
		refs(s.messages), refs(s.fields), s.oneofs, s.enums, s.values, s.services, s.methods)
	return s
}

func (s *schema) message(file, outer string, m *descriptorpb.DescriptorProto) {
	name := m.GetName()
	if outer != "" {
		name = outer + "." + name
	}
	at := file + ": " + name
	s.messages = append(s.messages, msgRef{ref{m.GetName(), at}, m})
	for _, f := range m.GetField() {
		s.fields = append(s.fields, fieldRef{ref{f.GetName(), at + "." + f.GetName()}, f})
	}
	for _, o := range m.GetOneofDecl() {
		s.oneofs = append(s.oneofs, ref{o.GetName(), at + ": oneof " + o.GetName()})
	}
	for _, e := range m.GetEnumType() {
		s.enum(file, name, e)
	}
	for _, n := range m.GetNestedType() {
		s.message(file, name, n)
	}
}

func (s *schema) enum(file, outer string, e *descriptorpb.EnumDescriptorProto) {
	name := e.GetName()
	if outer != "" {
		name = outer + "." + name
	}
	at := file + ": " + name
	s.enums = append(s.enums, ref{e.GetName(), at})
	for _, v := range e.GetValue() {
		s.values = append(s.values, ref{v.GetName(), at + "." + v.GetName()})
	}
}

func refs[T interface{ base() ref }](in []T) []ref {
	out := make([]ref, 0, len(in))
	for _, v := range in {
		out = append(out, v.base())
	}
	return out
}

func (r msgRef) base() ref   { return r.ref }
func (r fieldRef) base() ref { return r.ref }

// countProtoFiles counts what is on disk, excluding gen/ wherever it
// appears — the same scan the proto-v2 make target and tools/protogen run,
// and here it is the number buf's answer has to agree with.
func countProtoFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "gen" && p != dir {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".proto") {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return cwd
}
