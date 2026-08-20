package session

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/interp"
)

// WHAT A RESOURCE BECOMES, asked the same way everything else is asked.
//
// Some kinds are rewritten at compile time: a workflow is not run by anything, and what
// runs is the queues and functions it lowers to. A consumer needs that answer in two
// shapes, and both are here rather than in each consumer:
//
//	Lower(res)   what THIS resource becomes — the console's zoom-in, the CLI's dry run
//	Lowered()    the whole project after lowering — what actually gets deployed
//
// The address is the ordinary one: the same []string that Get, Set and ValidateResource
// take. A caller that can validate a resource can lower it, with nothing new to learn.

// DocumentLayout is a Layout that also says WHERE documents live. A lowering produces
// documents, so it needs the answer; a DSL that does not declare it cannot be lowered,
// which is honest rather than guessed at.
type DocumentLayout interface {
	Layout
	// ResourcePath is the document template for a resource, over {kind} and {name}.
	ResourcePath() string
	// ScopePath is the directory template for one level of scope, over {name}.
	ScopePath() string
}

// Lower is what one resource becomes: the documents it produces, and the entries it
// places on documents that already exist. A resource that lowers to nothing — most of
// them — returns an empty emission and no error, because "this becomes nothing" is an
// answer rather than a failure.
func (s *Session) Lower(ctx context.Context, res []string, opts CompileOptions) (interp.Emission, error) {
	layout, err := s.documentLayout()
	if err != nil {
		return interp.Emission{}, err
	}
	kind, name, scope, err := s.resourceAt(res)
	if err != nil {
		return interp.Emission{}, err
	}

	c, err := s.compiler(opts)
	if err != nil {
		return interp.Emission{}, err
	}
	obj, _, err := c.Compile(ctx)
	if err != nil {
		return interp.Emission{}, err
	}

	e, _ := interp.EmissionOf(c.Root(), obj.Flat(), layout, kind, name, scope)
	return e, nil
}

// Lowered is the whole project after lowering: the object a runtime is given, with the
// resources every lowering produced in it. The files are not touched — the view it
// compiles is in memory.
func (s *Session) Lowered(ctx context.Context, opts CompileOptions) (interp.Object, error) {
	layout, err := s.documentLayout()
	if err != nil {
		return nil, err
	}
	c, err := s.compiler(opts)
	if err != nil {
		return nil, err
	}
	lowered, err := c.Lowered(ctx, layout)
	if err != nil {
		return nil, err
	}
	obj, _, err := lowered.Compile(ctx)
	return obj, err
}

func (s *Session) documentLayout() (interp.Layout, error) {
	l, ok := s.bind.Layout.(DocumentLayout)
	if !ok {
		return interp.Layout{}, errors.New("session: this DSL does not say where its documents live, so nothing can be lowered")
	}
	return interp.Layout{Resource: l.ResourcePath(), Scope: l.ScopePath()}, nil
}

// resourceAt reads an ordinary resource address as the kind, name and scope a lowering
// is keyed on.
func (s *Session) resourceAt(res []string) (kind, name, scope string, err error) {
	bad := func(why string) error {
		return fmt.Errorf("session: %q is not a resource that can lower (%s)", strings.Join(res, "/"), why)
	}
	switch len(res) {
	case 2:
		k, err := s.kind(res[0])
		if err != nil {
			return "", "", "", err
		}
		return k.Group, res[1], "", nil
	case 4:
		if c, ok := s.container(); !ok || c.Group != res[0] {
			return "", "", "", bad(strconv.Quote(res[0]) + " is not a scope")
		}
		k, err := s.kind(res[2])
		if err != nil {
			return "", "", "", err
		}
		return k.Group, res[3], res[1], nil
	}
	return "", "", "", bad("a resource is a kind and a name, optionally inside a scope")
}
