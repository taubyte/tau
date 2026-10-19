package service

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/afero"

	"github.com/taubyte/tau/tools/spore-drive/config"
	pb "github.com/taubyte/tau/tools/spore-drive/proto/gen/drive/v2"
)

// document.go turns a Document — the config directory, file by file — into
// the parser drive reads, and back.
//
// THE FILES ARE THE FORMAT. An operator's config directory is read exactly as
// it sits on disk, so nothing here knows a field: config.Parser does, and a
// field it learns is learned once.

// notRead are documents of the on-disk layout a v2 node has no use for. They
// are accepted and reported, never refused: a config written for v1 keeps
// loading.
var notRead = []string{"accounts.yaml"}

// load writes a document into a fresh in-memory tree and parses it.
func load(doc *pb.Document) (afero.Fs, config.Parser, error) {
	if doc == nil || len(doc.GetFiles()) == 0 {
		return nil, nil, errors.New("an empty document")
	}

	tree := afero.NewMemMapFs()
	for name, content := range doc.GetFiles() {
		clean, err := relative(name)
		if err != nil {
			return nil, nil, err
		}
		if err := tree.MkdirAll(path.Dir("/"+clean), 0700); err != nil {
			return nil, nil, err
		}
		if err := afero.WriteFile(tree, "/"+clean, content, 0600); err != nil {
			return nil, nil, err
		}
	}

	p, err := config.New(tree, "/")
	if err != nil {
		return nil, nil, fmt.Errorf("parsing the document: %w", err)
	}
	return tree, p, nil
}

// relative is a file's path inside the config root, or why it is not one.
func relative(name string) (string, error) {
	clean := path.Clean(name)
	if name == "" || path.IsAbs(name) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%q is not a path inside the config", name)
	}
	return clean, nil
}

// dump reads the tree back into a document.
func dump(tree afero.Fs) (*pb.Document, error) {
	out := &pb.Document{Files: map[string][]byte{}}
	err := afero.Walk(tree, "/", func(name string, info fs.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		content, err := afero.ReadFile(tree, name)
		if err != nil {
			return err
		}
		out.Files[strings.TrimPrefix(name, "/")] = content
		return nil
	})
	return out, err
}

// missing is every secret the document leaves to be generated. Plan refuses a
// document with any; Validate generates them.
func missing(p config.Parser) []string {
	var out []string
	if !opens(p.Cloud().P2P().Swarm().Open) {
		out = append(out, "the swarm key")
	}
	v := p.Cloud().Domain().Validation()
	if !opens(v.OpenPrivateKey) || !opens(v.OpenPublicKey) {
		out = append(out, "the domain validation keys")
	}
	for _, h := range sorted(p.Hosts().List()) {
		for _, shape := range sorted(p.Hosts().Host(h).Shapes().List()) {
			if p.Hosts().Host(h).Shapes().Instance(shape).Key() == "" {
				out = append(out, "the key of host "+h+" for shape "+shape)
			}
		}
	}
	return out
}

// complete generates what missing names.
func complete(p config.Parser) error {
	if !opens(p.Cloud().P2P().Swarm().Open) {
		if err := p.Cloud().P2P().Swarm().Generate(); err != nil {
			return fmt.Errorf("generating the swarm key: %w", err)
		}
	}
	v := p.Cloud().Domain().Validation()
	if !opens(v.OpenPrivateKey) || !opens(v.OpenPublicKey) {
		if err := v.Generate(); err != nil {
			return fmt.Errorf("generating the domain validation keys: %w", err)
		}
	}
	for _, h := range p.Hosts().List() {
		for _, shape := range p.Hosts().Host(h).Shapes().List() {
			inst := p.Hosts().Host(h).Shapes().Instance(shape)
			if inst.Key() == "" {
				if err := inst.GenerateKey(); err != nil {
					return fmt.Errorf("generating the key of host %s for shape %s: %w", h, shape, err)
				}
			}
		}
	}
	return p.Sync()
}

func opens(open func() (io.ReadCloser, error)) bool {
	r, err := open()
	if err != nil {
		return false
	}
	r.Close()
	return true
}

// unread names the documents present that no v2 node reads.
func unread(doc *pb.Document) []string {
	var out []string
	for _, name := range notRead {
		if _, ok := doc.GetFiles()[name]; ok {
			out = append(out, name+" (no v2 node reads it)")
		}
	}
	return out
}

func sorted(in []string) []string {
	out := slices.Clone(in)
	sort.Strings(out)
	return out
}
