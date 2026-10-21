package frontend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/sideband"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// contentapi.go reads a path of a repository at a ref, and commits files to a
// branch, without the caller cloning anything: a console importing a project
// reads its config.yaml, and one creating a project writes the first one into
// a repository made seconds ago.
//
// BOTH ARE GIT, NOT A FORGE API, so every forge with a clone address answers
// them alike. The tip of the ref is fetched at depth one into memory, under
// the caller's connection through the same pinned transport /git/ uses, and
// dropped with the request. A commit is pushed with the tip it was built on
// as the old value, so a branch that moved meanwhile refuses it instead of
// losing what landed.

const (
	maxReadBody   = 8 << 10
	maxCommitBody = 16 << 20
	// maxCommitFiles bounds how many paths one commit may touch.
	maxCommitFiles = 64
)

type readRequest struct {
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
	Path       string `json:"path"`
}

type treeEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type readResponse struct {
	Repository string      `json:"repository"`
	Ref        string      `json:"ref"`
	Commit     string      `json:"commit"`
	Path       string      `json:"path"`
	Type       string      `json:"type"`
	Content    []byte      `json:"content,omitempty"`
	Entries    []treeEntry `json:"entries,omitempty"`
}

type commitFile struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
	Delete  bool   `json:"delete"`
}

type commitRequest struct {
	Repository string       `json:"repository"`
	Branch     string       `json:"branch"`
	Parent     string       `json:"parent"`
	Message    string       `json:"message"`
	Files      []commitFile `json:"files"`
}

type commitResponse struct {
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	Commit     string `json:"commit"`
}

var (
	errEmptyRepository = errors.New("the repository is empty")
	errNoRef           = errors.New("no such ref")
	errNoPath          = errors.New("no such path")
	errBranchMoved     = errors.New("the branch moved")
)

// contentKey reads the repository a content request names, on the forge its
// path names.
func contentKey(spelling, host string) (repokey.Key, error) {
	key, ok := parseRepositoryKey(spelling)
	if !ok {
		return key, problem(http.StatusBadRequest, "not a repository key: "+spelling)
	}
	if key.Host != host {
		return key, problem(http.StatusBadRequest, spelling+" is not on "+host)
	}
	return key, nil
}

// cleanPath is a repository path in git's own spelling: relative, slash
// separated, with no empty, "." or ".." segment and nothing under .git.
func cleanPath(p string, root bool) (string, bool) {
	p = strings.Trim(p, "/")
	if p == "" {
		return "", root
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.EqualFold(seg, ".git") || strings.ContainsAny(seg, "\x00\\") {
			return "", false
		}
	}
	return p, true
}

func contentProblem(err error) error {
	switch {
	case errors.Is(err, errEmptyRepository), errors.Is(err, errNoRef), errors.Is(err, errNoPath):
		return problem(http.StatusNotFound, err.Error())
	case errors.Is(err, errBranchMoved):
		return problem(http.StatusConflict, err.Error())
	case errors.Is(err, errFetchTooLarge):
		return problem(http.StatusRequestEntityTooLarge, err.Error())
	}
	return err
}

// remoteFor opens the caller's connection to key's host and the git remote
// it reaches key through.
func (s *Service) remoteFor(ctx context.Context, token string, key repokey.Key) (Remote, error) {
	forgeToken, err := s.openFor(ctx, token, key.Host)
	if err != nil {
		return Remote{}, err
	}
	remote, err := s.repos.Remote(ctx, forgeToken, key)
	if err != nil {
		return Remote{}, repoProblem(err)
	}
	return remote, nil
}

// handleRead is POST /forges/{forge}/read: one file's bytes, or one
// directory's entries, at a ref (the default branch when none is named).
func (s *Service) handleRead(w http.ResponseWriter, r *http.Request, token, host string) {
	if s.repos == nil {
		respond(w, errDenied)
		return
	}
	var req readRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReadBody)).Decode(&req); err != nil {
		respond(w, problem(http.StatusBadRequest, "body must be {repository, ref, path}"))
		return
	}
	key, err := contentKey(req.Repository, host)
	if err != nil {
		respond(w, err)
		return
	}
	p, ok := cleanPath(req.Path, true)
	if !ok {
		respond(w, problem(http.StatusBadRequest, "not a repository path: "+req.Path))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.GitTimeout)
	defer cancel()
	if err := s.readsKey(ctx, token, key); err != nil {
		respond(w, err)
		return
	}
	remote, err := s.remoteFor(ctx, token, key)
	if err != nil {
		respond(w, err)
		return
	}
	tip, err := fetchTip(ctx, remote, req.Ref, s.cfg.MaxFetchBytes)
	if err != nil {
		respond(w, contentProblem(err))
		return
	}
	if tip.empty {
		respond(w, contentProblem(errEmptyRepository))
		return
	}
	out := readResponse{Repository: key.String(), Ref: tip.ref.String(), Commit: tip.commit.Hash.String(), Path: p}
	tree, err := tip.commit.Tree()
	if err != nil {
		respond(w, err)
		return
	}
	if p != "" {
		entry, err := tree.FindEntry(p)
		if err != nil {
			respond(w, contentProblem(errNoPath))
			return
		}
		if entry.Mode != filemode.Dir {
			out.Type = entryType(entry.Mode)
			if entry.Mode == filemode.Submodule {
				writeJSON(w, http.StatusOK, out)
				return
			}
			blob, err := object.GetBlob(tip.st, entry.Hash)
			if err != nil {
				respond(w, err)
				return
			}
			rd, err := blob.Reader()
			if err != nil {
				respond(w, err)
				return
			}
			defer rd.Close()
			if out.Content, err = io.ReadAll(rd); err != nil {
				respond(w, err)
				return
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		if tree, err = object.GetTree(tip.st, entry.Hash); err != nil {
			respond(w, err)
			return
		}
	}
	out.Type = "tree"
	out.Entries = []treeEntry{}
	for _, e := range tree.Entries {
		out.Entries = append(out.Entries, treeEntry{Name: e.Name, Type: entryType(e.Mode)})
	}
	writeJSON(w, http.StatusOK, out)
}

func entryType(m filemode.FileMode) string {
	switch m {
	case filemode.Dir:
		return "tree"
	case filemode.Submodule:
		return "submodule"
	case filemode.Symlink:
		return "symlink"
	}
	return "file"
}

// handleCommit is POST /forges/{forge}/commit: one commit of the files named,
// on a branch (the default branch when none is named), built on its tip — or
// the first commit of an empty repository.
func (s *Service) handleCommit(w http.ResponseWriter, r *http.Request, token, host string) {
	if s.repos == nil {
		respond(w, errDenied)
		return
	}
	var req commitRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCommitBody)).Decode(&req); err != nil {
		if isTooLarge(err) {
			respond(w, errTooLarge)
			return
		}
		respond(w, problem(http.StatusBadRequest, "body must be {repository, branch, parent, message, files: [{path, content, delete}]}"))
		return
	}
	key, err := contentKey(req.Repository, host)
	if err != nil {
		respond(w, err)
		return
	}
	switch {
	case strings.TrimSpace(req.Message) == "":
		respond(w, problem(http.StatusBadRequest, "a commit needs a message"))
		return
	case len(req.Files) == 0 || len(req.Files) > maxCommitFiles:
		respond(w, problem(http.StatusBadRequest, fmt.Sprintf("a commit names between 1 and %d files", maxCommitFiles)))
		return
	case req.Branch != "" && plumbing.NewBranchReferenceName(req.Branch).Validate() != nil:
		respond(w, problem(http.StatusBadRequest, "not a branch: "+req.Branch))
		return
	case req.Parent != "" && !plumbing.IsHash(req.Parent):
		respond(w, problem(http.StatusBadRequest, "parent must be a full commit hash"))
		return
	}
	seen := map[string]bool{}
	for i, f := range req.Files {
		p, ok := cleanPath(f.Path, false)
		if !ok || seen[p] {
			respond(w, problem(http.StatusBadRequest, "not a path to commit, or named twice: "+f.Path))
			return
		}
		seen[p] = true
		req.Files[i].Path = p
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.GitTimeout)
	defer cancel()
	if err := s.writesAny(ctx, token, []string{key.String()}); err != nil {
		respond(w, err)
		return
	}
	remote, err := s.remoteFor(ctx, token, key)
	if err != nil {
		respond(w, err)
		return
	}
	branch := req.Branch
	ref := ""
	if branch != "" {
		ref = plumbing.NewBranchReferenceName(branch).String()
	}
	tip, err := fetchTip(ctx, remote, ref, s.cfg.MaxFetchBytes)
	switch {
	case errors.Is(err, errNoRef) && branch != "":
		// A NEW BRANCH starts from the default one.
		if tip, err = fetchTip(ctx, remote, "", s.cfg.MaxFetchBytes); err == nil && !tip.empty {
			tip.ref = plumbing.NewBranchReferenceName(branch)
			tip.old = plumbing.ZeroHash
		}
	case err == nil && branch == "" && !tip.empty && !tip.ref.IsBranch():
		err = fmt.Errorf("%w: the default ref %s is not a branch", errNoRef, tip.ref)
	}
	if err != nil {
		respond(w, contentProblem(err))
		return
	}
	if tip.empty {
		if branch == "" {
			branch = "main"
		}
		tip.ref = plumbing.NewBranchReferenceName(branch)
	}
	if req.Parent != "" && (tip.commit == nil || tip.commit.Hash.String() != req.Parent) {
		respond(w, contentProblem(fmt.Errorf("%w: %s is not at %s", errBranchMoved, tip.ref.Short(), req.Parent)))
		return
	}

	c := &committer{st: tip.st}
	root := plumbing.ZeroHash
	var parents []plumbing.Hash
	if tip.commit != nil {
		root = tip.commit.TreeHash
		parents = []plumbing.Hash{tip.commit.Hash}
	}
	for _, f := range req.Files {
		var blob *plumbing.Hash
		if !f.Delete {
			h, err := c.blob(f.Content)
			if err != nil {
				respond(w, err)
				return
			}
			blob = &h
		}
		if root, err = c.put(root, strings.Split(f.Path, "/"), blob); err != nil {
			respond(w, err)
			return
		}
	}
	if root.IsZero() {
		if root, err = c.tree(nil); err != nil {
			respond(w, err)
			return
		}
	}
	if tip.commit != nil && root == tip.commit.TreeHash {
		respond(w, problem(http.StatusConflict, "nothing to commit: "+tip.ref.Short()+" already holds those files"))
		return
	}
	who := object.Signature{Name: remote.Author, Email: remote.Email, When: s.now()}
	commit, err := c.commit(&object.Commit{Author: who, Committer: who, Message: req.Message, TreeHash: root, ParentHashes: parents})
	if err != nil {
		respond(w, err)
		return
	}
	if err := push(ctx, remote, tip.ref, tip.old, commit, c); err != nil {
		if !errors.Is(err, errBranchMoved) {
			s.log("committing to %s: %v", key, err)
		}
		respond(w, contentProblem(err))
		return
	}
	writeJSON(w, http.StatusCreated, commitResponse{Repository: key.String(), Branch: tip.ref.Short(), Commit: commit.String()})
}

// ── git, in memory ───────────────────────────────────────────────────────────

var errFetchTooLarge = errors.New("the repository is too large to read here; clone it through /git/ instead")

type tip struct {
	st     *memory.Storage
	ref    plumbing.ReferenceName
	old    plumbing.Hash
	commit *object.Commit
	empty  bool
}

func gitTransport(remote Remote) transport.Transport {
	return githttp.NewClient(&http.Client{
		Transport: remote.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	})
}

// fetchTip fetches ref's tip at depth one: a branch, a tag, or a full ref
// name, and the remote's HEAD when ref is empty.
func fetchTip(ctx context.Context, remote Remote, ref string, maxBytes int64) (*tip, error) {
	ep, err := transport.NewEndpoint(remote.URL.String())
	if err != nil {
		return nil, err
	}
	sess, err := gitTransport(remote).NewUploadPackSession(ep, nil)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	adv, err := sess.AdvertisedReferencesContext(ctx)
	switch {
	case errors.Is(err, transport.ErrEmptyRemoteRepository):
		return &tip{st: memory.NewStorage(), empty: true}, nil
	case err != nil:
		return nil, err
	}
	refs, err := adv.AllReferences()
	if err != nil {
		return nil, err
	}
	out := &tip{st: memory.NewStorage()}
	switch {
	case ref == "" && adv.Head != nil:
		out.ref = plumbing.HEAD
		for _, target := range adv.Capabilities.Get(capability.SymRef) {
			if from, to, ok := strings.Cut(target, ":"); ok && from == "HEAD" {
				out.ref = plumbing.ReferenceName(to)
			}
		}
		out.old = *adv.Head
	case ref == "" && len(refs) == 0:
		out.empty = true
		return out, nil
	case ref == "":
		return nil, fmt.Errorf("%w: the repository has no default branch", errNoRef)
	default:
		for _, name := range []string{ref, "refs/heads/" + ref, "refs/tags/" + ref} {
			if r, ok := refs[plumbing.ReferenceName(name)]; ok && r.Type() == plumbing.HashReference {
				out.ref, out.old = r.Name(), r.Hash()
				break
			}
		}
		if out.ref == "" {
			return nil, fmt.Errorf("%w: %s", errNoRef, ref)
		}
	}

	req := packp.NewUploadPackRequestFromCapabilities(adv.Capabilities)
	req.Wants = []plumbing.Hash{out.old}
	req.Depth = packp.DepthCommits(1)
	if err := req.Capabilities.Set(capability.Shallow); err != nil {
		return nil, err
	}
	if adv.Capabilities.Supports(capability.NoProgress) {
		if err := req.Capabilities.Set(capability.NoProgress); err != nil {
			return nil, err
		}
	}
	resp, err := sess.UploadPack(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Close()
	var pack io.Reader = &capped{r: resp, left: maxBytes}
	switch {
	case req.Capabilities.Supports(capability.Sideband64k):
		pack = sideband.NewDemuxer(sideband.Sideband64k, pack)
	case req.Capabilities.Supports(capability.Sideband):
		pack = sideband.NewDemuxer(sideband.Sideband, pack)
	}
	if err := packfile.UpdateObjectStorage(out.st, pack); err != nil {
		return nil, err
	}
	commit, err := peel(out.st, out.old)
	if err != nil {
		return nil, err
	}
	out.commit = commit
	return out, nil
}

// peel is the commit h names, through any annotated tags.
func peel(st *memory.Storage, h plumbing.Hash) (*object.Commit, error) {
	for range 8 {
		obj, err := object.GetObject(st, h)
		if err != nil {
			return nil, err
		}
		switch o := obj.(type) {
		case *object.Commit:
			return o, nil
		case *object.Tag:
			h = o.Target
		default:
			return nil, fmt.Errorf("%w: %s names a %s, not a commit", errNoRef, h, obj.Type())
		}
	}
	return nil, fmt.Errorf("%w: %s is tags all the way down", errNoRef, h)
}

// capped fails a read past its limit instead of truncating it.
type capped struct {
	r    io.Reader
	left int64
}

func (c *capped) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, errFetchTooLarge
	}
	if int64(len(p)) > c.left {
		p = p[:c.left+1]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	if c.left < 0 {
		return n, errFetchTooLarge
	}
	return n, err
}

// committer writes the objects of one commit and remembers which, since they
// are exactly what the push sends.
type committer struct {
	st      *memory.Storage
	written []plumbing.Hash
}

func (c *committer) store(obj plumbing.EncodedObject) (plumbing.Hash, error) {
	h, err := c.st.SetEncodedObject(obj)
	if err == nil {
		c.written = append(c.written, h)
	}
	return h, err
}

func (c *committer) blob(content []byte) (plumbing.Hash, error) {
	obj := c.st.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	obj.SetSize(int64(len(content)))
	wr, err := obj.Writer()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if _, err := wr.Write(content); err != nil {
		return plumbing.ZeroHash, err
	}
	if err := wr.Close(); err != nil {
		return plumbing.ZeroHash, err
	}
	return c.store(obj)
}

func (c *committer) tree(entries []object.TreeEntry) (plumbing.Hash, error) {
	sort.Sort(object.TreeEntrySorter(entries))
	obj := c.st.NewEncodedObject()
	if err := (&object.Tree{Entries: entries}).Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}
	return c.store(obj)
}

func (c *committer) commit(commit *object.Commit) (plumbing.Hash, error) {
	obj := c.st.NewEncodedObject()
	if err := commit.Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}
	return c.store(obj)
}

// put is the tree at, with segs set to blob (removed when blob is nil). The
// zero hash is the empty tree, which git never stores under a directory.
func (c *committer) put(at plumbing.Hash, segs []string, blob *plumbing.Hash) (plumbing.Hash, error) {
	var entries []object.TreeEntry
	if !at.IsZero() {
		t, err := object.GetTree(c.st, at)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		entries = append(entries, t.Entries...)
	}
	name := segs[0]
	var existing *object.TreeEntry
	kept := entries[:0]
	for i := range entries {
		if entries[i].Name == name {
			e := entries[i]
			existing = &e
			continue
		}
		kept = append(kept, entries[i])
	}
	entries = kept
	if len(segs) == 1 {
		if existing != nil && existing.Mode == filemode.Dir {
			return plumbing.ZeroHash, problem(http.StatusConflict, name+" is a directory")
		}
		if blob != nil {
			mode := filemode.Regular
			if existing != nil && existing.Mode == filemode.Executable {
				mode = filemode.Executable
			}
			entries = append(entries, object.TreeEntry{Name: name, Mode: mode, Hash: *blob})
		}
	} else {
		sub := plumbing.ZeroHash
		if existing != nil {
			if existing.Mode != filemode.Dir {
				return plumbing.ZeroHash, problem(http.StatusConflict, name+" is a file, not a directory")
			}
			sub = existing.Hash
		}
		next, err := c.put(sub, segs[1:], blob)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		if !next.IsZero() {
			entries = append(entries, object.TreeEntry{Name: name, Mode: filemode.Dir, Hash: next})
		}
	}
	if len(entries) == 0 {
		return plumbing.ZeroHash, nil
	}
	return c.tree(entries)
}

// push moves ref from old to commit, sending only what c wrote: everything
// else the commit reaches is already the remote's.
func push(ctx context.Context, remote Remote, ref plumbing.ReferenceName, old, commit plumbing.Hash, c *committer) error {
	ep, err := transport.NewEndpoint(remote.URL.String())
	if err != nil {
		return err
	}
	sess, err := gitTransport(remote).NewReceivePackSession(ep, nil)
	if err != nil {
		return err
	}
	defer sess.Close()
	adv, err := sess.AdvertisedReferencesContext(ctx)
	if err != nil && !errors.Is(err, transport.ErrEmptyRemoteRepository) {
		return err
	}
	capabilities := capability.NewList()
	if adv != nil {
		capabilities = adv.Capabilities
		refs, err := adv.AllReferences()
		if err != nil {
			return err
		}
		now := plumbing.ZeroHash
		if r, ok := refs[ref]; ok {
			now = r.Hash()
		}
		if now != old {
			return fmt.Errorf("%w: %s is no longer at %s", errBranchMoved, ref.Short(), old)
		}
	}
	var pack bytes.Buffer
	if _, err := packfile.NewEncoder(&pack, c.st, false).Encode(c.written, 0); err != nil {
		return err
	}
	req := packp.NewReferenceUpdateRequestFromCapabilities(capabilities)
	req.Commands = []*packp.Command{{Name: ref, Old: old, New: commit}}
	req.Packfile = io.NopCloser(&pack)
	if capabilities.Supports(capability.Sideband64k) {
		if err := req.Capabilities.Set(capability.Sideband64k); err != nil {
			return err
		}
	}
	report, err := sess.ReceivePack(ctx, req)
	if err != nil {
		return err
	}
	if report != nil {
		if err := report.Error(); err != nil {
			if strings.Contains(err.Error(), "fetch first") || strings.Contains(err.Error(), "non-fast-forward") || strings.Contains(err.Error(), "stale info") {
				return fmt.Errorf("%w: %v", errBranchMoved, err)
			}
			return err
		}
	}
	return nil
}
