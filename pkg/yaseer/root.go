package seer

import (
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

func (s *Seer) Sync() error {
	s.lock.Lock()
	defer s.lock.Unlock()

	for docName, doc := range s.documents {
		f, err := s.fs.OpenFile(docName, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0640)
		if err != nil {
			return fmt.Errorf("opening %s failed with %w", docName, err)
		}
		defer f.Close()

		enc := yaml.NewEncoder(f)
		err = enc.Encode(doc)
		if err != nil {
			return fmt.Errorf("encoding data to %s failed with %w", docName, err)
		}
	}
	return nil
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
	return &Query{
		seer:     s,
		ops:      make([]op, 0),
		errors:   make([]error, 0),
		filePath: "",
		line:     0,
		column:   0,
	}
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

func parseYAMLError(err error) (line, column int) {
	if err == nil {
		return 0, 0
	}

	errStr := err.Error()
	re := regexp.MustCompile(`line (\d+):\s*column (\d+)`)
	matches := re.FindStringSubmatch(errStr)
	if len(matches) == 3 {
		if l, err := strconv.Atoi(matches[1]); err == nil {
			line = l
		}
		if c, err := strconv.Atoi(matches[2]); err == nil {
			column = c
		}
		return line, column
	}

	re = regexp.MustCompile(`line (\d+)`)
	matches = re.FindStringSubmatch(errStr)
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
	return root_node, nil
}
