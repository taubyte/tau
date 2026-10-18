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

// EVERY RULE THIS PROJECT ACTUALLY CARES ABOUT IN A .proto IS CHECKED HERE,
// BECAUSE buf CHECKS NONE OF THEM.
//
// buf lint reads style: is the file's directory its package, is a field
// lower_snake_case, does an enum's zero value carry the right suffix. It has
// no opinion whatever on what the schema SAYS. Nine semantic mutations were
// applied to build.proto before this test existed — a numeric repository id,
// a `oneof` over forge names, the webhook's changed-path list on the message
// that crosses the queue, RPCs named Clone and RunContainer — and all nine
// passed `buf lint`, `buf build` and `buf generate` untouched. A decision
// recorded in .notes/v2/ and then written into a doc comment is a decision
// nothing enforces; this file is where the ones that can be stated
// mechanically stop being prose.
//
// It walks the compiled FileDescriptorSet — `buf build -o` on the module,
// then descriptorpb — rather than the .proto text, because the descriptor is
// what the wire is actually generated from: a rule evaluated against it
// cannot be dodged by a comment, an import, or a formatting choice. Every v2
// proto is walked, not just the newest one. The rules and the reasons:
//
//	§1  a repository is one string key            implementation-gap.md §16, §17/D22
//	§2  nothing is named for a v1 service         implementation-gap.md decision 15
//	§3  package, directory and protocol id agree  service-conventions.md §2d
//	§4  no provider webhook payload crosses       service-topology.md §5 (D5)
//	§5  no build capability is an RPC             service-topology.md §2
//	§6  v2's schema imports only v2's schema      buf.gen.yaml's own doc
//	§7  no file declares go_package               commit a4a7668d
//
// WHAT IT CANNOT CHECK is listed in TestTheRulesBufCannotCheck's last
// subtest, by name, because a guard that looks complete is worse than one
// that says where it stops.
//
// Like pkg/tcc/taubyte/v2/schema's boundary test, most of this passes
// vacuously on the day it is written. That is when it is worth writing: the
// tree is clean exactly once.
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

	// The count keeps the whole file from going quiet the way tools/tcc-gen's
	// check once did at 2 files of 57 (commit 481fab44), and the way an
	// uncommitted gen/ made the drift check pass by having no opinion
	// (commit a4a7668d). If buf ever builds fewer files than are on disk,
	// every rule below is being asked about a smaller tree than it thinks.
	if s.onDisk == 0 || len(s.files) != s.onDisk {
		t.Fatalf("buf built %d files from a module holding %d .proto sources on disk — "+
			"every rule below would be checking a tree that isn't there", len(s.files), s.onDisk)
	}

	// ─── §1. A repository is one string key ────────────────────────────
	//
	// implementation-gap.md §17, decision 22: the key is a normalized clone
	// URL, `host/owner/repo`, computable offline by both sides. §16 killed
	// the alternatives in the config DSL and gives the reason in one line —
	// "The URL is the provider… a clone URL carries the host, so GitLab,
	// Gitea, Codeberg and an internal mirror are values, not branches. The
	// numeric repo id went with it: a GitHub fact, not a git one." An
	// `Either("github")` switch closed the vocabulary to one forge and made
	// the second one a schema edit; a proto `oneof` over forge names is that
	// same switch, and a numeric id is the field it took with it.
	//
	// THE RULE IS ABOUT REPOSITORY IDENTITY, AND IT IS CALIBRATED AGAINST
	// THE TREE RATHER THAN AGAINST ONE FILE. auth.proto carries a
	// `GithubHook` message and an `int64 provider_hook_id`, and both are
	// correct: a webhook is a resource GITHUB owns and numbers, so nothing
	// could derive that id, where a repository's key is derived from its own
	// clone URL. auth.proto argues exactly that distinction in place. What
	// is refused below is the forge or the integer standing in for the
	// repository — never a message that mentions a forge it genuinely
	// describes.
	t.Run("ARepositoryIsAStringKeyAndNeverANumberOrAForge", func(t *testing.T) {
		var found []string
		for _, f := range s.fields {
			switch {
			case numeric(f.d) && hasSegment(f.name, "repo", "repository") != "":
				found = append(found, f.where+" is a numeric repository id; "+
					"the key is a string (implementation-gap.md §17, decision 22)")
			case numeric(f.d) && hasSegment(f.name, forges...) != "":
				found = append(found, f.where+" is a forge's own number standing in for identity "+
					"(implementation-gap.md §16, and work-order.md:92's `int64 github = 1`)")
			}
		}
		for _, o := range s.oneofs {
			if hasSegment(o.name, "repo", "repository", "provider") != "" {
				found = append(found, o.where+" branches on where a repository lives; "+
					"the host in the key already says (implementation-gap.md §16)")
			}
		}
		// "never a `provider` field beside a repository key" is auth.proto's
		// own phrasing and build.proto restates it: two fields that must
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

	// ─── §2. Nothing is named for a v1 service ─────────────────────────
	//
	// implementation-gap.md, decision 15: "v2 does not carry the names
	// patrick, monkey, hoarder or substrate." service-topology.md §1 names
	// the successors — worker, storage, and one build service with two
	// roles — and says the targets are not open any more. The names survive
	// in the tree as citations of v1 code and as v1's own frozen key
	// prefixes and topics (legacy/specs, pkg/tcc/internal/parity), which is
	// why this looks at the descriptor's NAMES and not at its comments:
	// build.proto cites services/patrick/api.go a dozen times and should.
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
		refuse(t, "§2 v2 does not carry v1's service names (implementation-gap.md, decision 15)", found)
	})

	// ─── §3. Package, directory and protocol id agree ──────────────────
	//
	// service-conventions.md §2d, whose table gives three rows — auth ->
	// tau.auth.v2 -> /tau/auth/v2, tns, build — and one rule: the protocol
	// id is "/" plus the proto package with dots as slashes, derived
	// (rpc.Protocol) and never typed twice. The "tau." segment is the
	// anti-collision half. It is not decoration: `package auth.v2` would
	// derive "/auth/v2", a bare service name on a swarm that carries other
	// protocols too, and v1's ids (/auth/v1, /tns/v1, /seer/v1, ...) were
	// exactly that shape. packageShape is what keeps the "tau." segment.
	//
	// The directory half is buf's PACKAGE_DIRECTORY_MATCH as well, but
	// buf.yaml's lint `except:` list is one line away from not being, and
	// the import path the generated Go lands on follows the directory.
	t.Run("PackageDirectoryAndProtocolIdAgree", func(t *testing.T) {
		var found []string
		for _, f := range s.files {
			pkg := f.GetPackage()
			if !packageShape.MatchString(pkg) {
				found = append(found, f.GetName()+" declares package "+strconv.Quote(pkg)+
					", not tau.<service>.v<N> (service-conventions.md §2d)")
				continue
			}
			if dir := path.Dir(f.GetName()); dir != strings.ReplaceAll(pkg, ".", "/") {
				found = append(found, f.GetName()+" sits at "+dir+" but declares package "+pkg)
			}
		}
		refuse(t, "§3 the protocol id is the package, and it may not be one v1 holds", found)
	})

	// ─── §4. No provider webhook payload crosses ───────────────────────
	//
	// service-topology.md §5, deciding D5: the payload's per-commit
	// `added|modified|removed` is provider-shaped and truncates past 20
	// commits or 3000 files, and the public/private split adds the argument
	// the other two axes did not have — forwarding it pushes
	// attacker-controlled data from an internet-facing listener into the
	// backend's path-filter logic. The backend computes its own
	// before..after diff after the clone instead. The same ruling takes
	// `head_commit`, `pushed_at` and the payload's clone URLs with it: each
	// is either a fact the backend re-derives or one it already has twice.
	//
	// The changed-path names are refused only when the field is REPEATED —
	// a path list is, and an ordinary `modified_at` timestamp is not. That
	// is the discriminator rather than an exemption list.
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
			found = append(found, f.where+" is the provider's webhook payload, not the repository's own answer "+
				"(service-topology.md §5, D5)")
		}
		refuse(t, "§4 what a stranger's POST said does not cross the queue", found)
	})

	// ─── §5. No build capability is an RPC ─────────────────────────────
	//
	// service-topology.md §2 splits build on a TRUST boundary and lists the
	// capabilities each side holds: the frontend "needs no container
	// runtime, no clone credentials, no build images, no publish rights",
	// the backend needs all four and no ingress.
	//
	// THIS IS A PROXY AND IS MEANT TO BE READ AS ONE. Which role serves a
	// given service block is not a fact the descriptor carries — see the
	// gaps subtest. What the descriptor does carry is the method names, and
	// a working copy and a container runtime are things a role EXERCISES
	// with its own credentials, in its own process, never things one tau
	// node asks another to do: build.proto makes the same argument for job
	// claiming, which is consensus rather than a message. So an RPC named
	// for one of them is either the capability leaking to the wrong side of
	// the boundary or a service-internal operation that should never have
	// been on an inter-service surface, and both are worth failing over.
	// "publish" is deliberately NOT on this list: a Publish or Push RPC is
	// exactly what tns will need, and a rule that would refuse it is a rule
	// somebody deletes rather than argues with.
	t.Run("NoBuildCapabilityIsAnRPC", func(t *testing.T) {
		var found []string
		for _, m := range s.methods {
			if w := hasSegment(m.name, "clone", "checkout", "container"); w != "" {
				found = append(found, m.where+" puts "+w+" on the wire; a working copy and a "+
					"container runtime are exercised in-process by the role that holds the "+
					"credentials, never asked for over RPC (service-topology.md §2)")
			}
		}
		refuse(t, "§5 a build capability is held, not offered", found)
	})

	// ─── §6. v2's schema imports only v2's schema ──────────────────────
	//
	// buf.gen.yaml's own doc: cross-service types belong in
	// tau/common/v1/common.proto, imported the way drive.proto imports
	// config.proto — "v2's own, never legacy's pkg/specs address space".
	// buf.yaml puts this module in a workspace with pkg/taucorder/proto,
	// tools/spore-drive/proto and ee/proto, so an import across that boundary
	// resolves and builds clean. It also arrives with everything that file
	// declares: `taucorder.v1.RepositoryId` is `oneof id { int64 github = 1;
	// string bitbucket = 2; }` — §1's whole subject, both halves of it, in
	// one import. Refusing the import is refusing that, and the check is
	// against what this module actually declares on disk rather than a
	// prefix, so a v1 tree that ever moved under tau/ would not be waved
	// through. This is pkg/tcc/taubyte/v2's boundary test one layer along,
	// in the other generator.
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

	// ─── §7. No file declares go_package ───────────────────────────────
	//
	// buf's managed mode sets it from go_package_prefix (buf.gen.yaml), so a
	// declaration in the file does nothing at all — which is how commit
	// a4a7668d found `option go_package = "."` still sitting in auth.proto
	// after the file moved into this module. A dead option that reads as
	// live is how someone ends up believing the wrong import path.
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

	// ─── What this file cannot check ───────────────────────────────────
	//
	// Named rather than quietly missing. Each of these is a real decision in
	// .notes/v2/ that a FileDescriptorSet has no way to express, and each is
	// listed with where the check actually belongs.
	//
	//  1. WHICH ROLE SERVES A SERVICE BLOCK. build.proto's central claim is
	//     that every RPC in it is served by the private backend and that the
	//     public frontend hosts no RPC surface at all. A descriptor knows
	//     packages, services and methods; it does not know processes. §5
	//     above refuses the capability NAMES a frontend must not hold, which
	//     is the nearest mechanical thing, and it would not catch a
	//     harmless-looking RPC moved to the wrong side. The property becomes
	//     checkable when the roles have descriptors in Go: two entries or
	//     one with a role field is still open (service-topology.md §6).
	//
	//  2. WHICH SERVICES FACE THE INTERNET. service-topology.md §2's
	//     invariant — exactly two, gateway and the build frontend — lives in
	//     Descriptor.Ingress (service-conventions.md §5a), which is Go, and
	//     its test belongs in the node registry, the only place the whole
	//     set is visible. Not here, and not in the schema: a .proto cannot
	//     say who binds a socket.
	//
	//  3. "NEEDS CLONE CREDENTIALS." §2's capability lists are about what a
	//     process holds, which no wire format states. The mechanical half
	//     that does exist is service-conventions.md §5a's other check — a
	//     service with no ingress imports no pkg/http — and it is a Go
	//     import walk, the shape pkg/tcc/taubyte/v2/schema's boundary test
	//     already runs.
	//
	//  4. WHO MAY CALL AN RPC. Authorization is §5c's, read from the
	//     libp2p-authenticated caller in the service's own authz.go. A
	//     schema that tried to carry it would be carrying the identity the
	//     caller encodes, which is the thing §5b exists to refuse.
	//
	//  5. WHAT A STRING MEANS. `repository_key` is a repokey and `ref` is a
	//     git ref because the doc comments say so; to the descriptor both
	//     are string. §1 can refuse the wrong TYPE for an identity and
	//     cannot confirm the right one.
	//
	//  6. `before` AND `after`. D5 rules both out of the crossing and
	//     build.proto explains why, but they are ordinary enough words —
	//     a time range, a pagination cursor — that refusing them by name
	//     would be a rule someone deletes rather than argues with. §4 takes
	//     the names that are unambiguously the provider's.
	t.Run("WhatThisCannotCheck", func(t *testing.T) {
		for _, gap := range []string{
			"which ROLE serves a service block — §5 refuses capability names as a proxy",
			"which services face the internet — Descriptor.Ingress, tested at the node registry",
			"which process holds clone credentials — not a fact any wire format states",
			"who may call an RPC — authz.go, from the libp2p-authenticated caller",
			"what a string means — `repository_key` is a repokey only in the doc comment",
			"`before` and `after` — ruled out by D5, too ordinary to refuse by name",
		} {
			t.Log("NOT CHECKED HERE: " + gap)
		}
	})
}

// ─────────────────────────── the vocabulary ───────────────────────────

var (
	// forges: every one of these is a value in a clone URL's host, never a
	// branch in a schema (implementation-gap.md §16).
	forges = []string{"github", "gitlab", "bitbucket", "gitea", "codeberg", "gogs", "forgejo", "sourcehut"}

	// v1Services: implementation-gap.md, decision 15.
	v1Services = []string{"patrick", "monkey", "hoarder", "substrate"}

	// packageShape: service-conventions.md §2d's table, as a pattern. The
	// trailing version is what makes a breaking change a new package, a new
	// protocol id and a second listener beside the old one.
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

// ──────────────────────── the compiled surface ────────────────────────

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
