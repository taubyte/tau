package seer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
)

func (s *Seer) Batch(queries ...*Query) *Batch {
	b := &Batch{
		queries: make([]*Query, len(queries)),
	}

	copy(b.queries, queries)

	return b
}

// Sync flushes every staged document to disk and clears the WAL.
//
// With WAL enabled (see WithWAL), commits that happened since the
// last Sync are already durable in the log — they get replayed on
// the next New() if we crash here. Sync's job is just to materialise
// the resulting in-memory state into the actual YAML files and then
// truncate the log, since the per-commit frames are no longer needed
// once the data files are on disk.
//
// Two-step: acquire the seer lock, then call syncLocked. Splitting
// the lock from the work lets replayWAL drive Sync internally after
// re-running ops without re-acquiring.
func (s *Seer) Sync() error {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.syncLocked()
}

// syncLocked writes every dirty document to its real path then
// truncates the WAL. Caller must hold s.lock.
func (s *Seer) syncLocked() error {
	for path := range s.dirty {
		doc, ok := s.documents[path]
		if !ok {
			// Deleted after being marked dirty — nothing to write.
			continue
		}
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		if err := enc.Encode(doc); err != nil {
			return fmt.Errorf("encoding %s failed with %w", path, err)
		}
		if err := enc.Close(); err != nil {
			return fmt.Errorf("closing encoder for %s failed with %w", path, err)
		}
		f, err := s.fs.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o640)
		if err != nil {
			return fmt.Errorf("opening %s failed with %w", path, err)
		}
		if _, err := f.Write(buf.Bytes()); err != nil {
			f.Close()
			return fmt.Errorf("writing %s failed with %w", path, err)
		}
		if syncer, ok := f.(interface{ Sync() error }); ok {
			if err := syncer.Sync(); err != nil {
				f.Close()
				return fmt.Errorf("fsync %s failed with %w", path, err)
			}
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("closing %s failed with %w", path, err)
		}
	}
	// Everything staged is now on disk; reset the dirty set and drop the
	// now-redundant per-commit WAL frames for the next batch of commits — unless
	// the WAL is a retained op-log (see WithWALRetain / ReplayInto), in which case
	// its frames must survive Sync so they can be replayed elsewhere.
	clear(s.dirty)
	if s.memwal != nil {
		return nil // in-mem WAL is a retained op-log, not per-Sync durability
	}
	return s.clearWAL()
}

func (s *Seer) Get(name string) *Query {
	return s.Query().Get(name)
}

func (s *Seer) List() ([]string, error) {

	list, err := afero.ReadDir(s.fs, "/")
	if err != nil {
		return nil, fmt.Errorf("listing seer's root failed with %w", err)
	}

	out := make([]string, 0)
	for _, s := range list {
		name := s.Name()
		if s.IsDir() {
			out = append(out, name)
		} else if strings.HasSuffix(name, ".yaml") {
			out = append(out, strings.TrimSuffix(name, ".yaml"))
		}
	}

	return out, nil
}

func (s *Seer) Query() *Query {
	return &Query{seer: s}
}

type YAMLError struct {
	FilePath string
	Line     int
	Column   int
	Err      error
}

func (e *YAMLError) Error() string {
	if e.Line > 0 && e.Column > 0 {
		return fmt.Sprintf("error parsing YAML file '%s' at line %d, column %d: %v", e.FilePath, e.Line, e.Column, e.Err)
	} else if e.Line > 0 {
		return fmt.Sprintf("error parsing YAML file '%s' at line %d: %v", e.FilePath, e.Line, e.Err)
	}
	return fmt.Sprintf("error parsing YAML file '%s': %v", e.FilePath, e.Err)
}

func (e *YAMLError) Unwrap() error {
	return e.Err
}

var (
	reYAMLLineColumn = regexp.MustCompile(`line (\d+):\s*column (\d+)`)
	reYAMLLine       = regexp.MustCompile(`line (\d+)`)
)

func parseYAMLError(err error) (line, column int) {
	if err == nil {
		return 0, 0
	}

	errStr := err.Error()
	matches := reYAMLLineColumn.FindStringSubmatch(errStr)
	if len(matches) == 3 {
		if l, err := strconv.Atoi(matches[1]); err == nil {
			line = l
		}
		if c, err := strconv.Atoi(matches[2]); err == nil {
			column = c
		}
		return line, column
	}

	matches = reYAMLLine.FindStringSubmatch(errStr)
	if len(matches) == 2 {
		if l, err := strconv.Atoi(matches[1]); err == nil {
			line = l
		}
	}

	return line, column
}

func (s *Seer) loadYamlDocument(path string) (*yaml.Node, error) {
	f, err := s.fs.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening yaml file %s failed with %w", path, err)
	}
	defer f.Close()

	root_node := &yaml.Node{}
	yaml_decoder := yaml.NewDecoder(f)
	err = yaml_decoder.Decode(root_node)
	if err != nil && !errors.Is(err, io.EOF) {
		line, column := parseYAMLError(err)
		return nil, &YAMLError{
			FilePath: path,
			Line:     line,
			Column:   column,
			Err:      err,
		}
	}

	s.documents[path] = root_node
	// A read that loads a previously-absent document changes what later
	// resolutions see, so invalidate read memos taken before this load.
	s.gen++
	return root_node, nil
}
