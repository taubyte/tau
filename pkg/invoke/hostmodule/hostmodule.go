// Package hostmodule is a host interface defined in WIT and implemented once, in Go,
// for every engine that runs guest code: wazy for components, warp for scripts.
//
// A module's functions take and return Values, which say nothing about any engine.
// Each engine's binder reads the types from the module's WIT and converts, so a
// module is written once and attached anywhere a WIT-reading engine runs.
package hostmodule

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/samyfodil/wazy/component"
	"github.com/samyfodil/wazy/component/wit"
)

// Value is a WIT value in Go:
//
//	bool                       bool
//	s8, s16, s32               int32
//	u8, u16, u32               uint32
//	s64, u64                   int64, uint64
//	f32, f64                   float32, float64
//	char, string               rune, string
//	list<u8>                   []byte
//	any other list, a tuple    []Value
//	record                     Record, by field name
//	variant                    Variant, by case name
//	enum                       string, the case
//	flags                      []string, the flags set
//	option<T>                  nil for none, the value for some
//	result<T, E>               Result
type Value = any

// Record is a WIT record by field name.
type Record map[string]Value

// Variant is one case of a WIT variant, and its payload, nil for a case that carries
// none.
type Variant struct {
	Case    string
	Payload Value
}

// Result is a WIT result: Err selects the arm, and Payload is its value, nil for an arm
// declared without one.
type Result struct {
	Err     bool
	Payload Value
}

// Ok is the ok arm of a result.
func Ok(payload Value) Result { return Result{Payload: payload} }

// Fail is the error arm of a result.
func Fail(payload Value) Result { return Result{Err: true, Payload: payload} }

// Func is one function: its arguments in WIT order, and its result, nil for a function
// that returns nothing. An error is not the WIT's: it traps the guest. A failure the
// WIT declares is a Result.
type Func func(ctx context.Context, args []Value) (Value, error)

// Definition is one interface, read from its WIT once and shared by every module that
// implements it.
type Definition struct {
	name  string
	fsys  fs.FS
	dir   string
	funcs []string
	sigs  map[string]*wit.Signature
}

var (
	// ErrUnsupported is a WIT type no binder converts yet.
	ErrUnsupported = errors.New("hostmodule: a WIT type this package does not convert yet")

	// ErrIncomplete is a module that does not implement exactly its interface's
	// functions.
	ErrIncomplete = errors.New("hostmodule: a module must implement its interface's functions, and only those")
)

// Define reads interface name from the WIT package in dir of fsys, with that
// package's deps/ beside it.
func Define(fsys fs.FS, dir, name string) (*Definition, error) {
	pkg, err := wit.ParseFS(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("hostmodule: %w", err)
	}
	iface := pkg.Interface(name)
	if iface == nil {
		return nil, fmt.Errorf("hostmodule: %s declares no interface %s", dir, name)
	}
	if len(iface.Resources) > 0 {
		return nil, fmt.Errorf("%w: %s declares a resource", ErrUnsupported, name)
	}

	d := &Definition{name: iface.Name, fsys: fsys, dir: dir, sigs: map[string]*wit.Signature{}}
	for _, f := range iface.Funcs {
		sig, err := f.Signature(nil)
		if err != nil {
			return nil, fmt.Errorf("hostmodule: %s.%s: %w", name, f.Name, err)
		}
		resolve := sig.Types.Resolver()
		for _, p := range sig.Desc.Params {
			if err := supported(p.Type, resolve); err != nil {
				return nil, fmt.Errorf("%s.%s param %s: %w", name, f.Name, p.Name, err)
			}
		}
		if r := sig.Desc.Results.Unnamed; r != nil {
			if err := supported(*r, resolve); err != nil {
				return nil, fmt.Errorf("%s.%s result: %w", name, f.Name, err)
			}
		}
		if len(sig.Desc.Results.Named) > 0 || sig.Desc.Async {
			return nil, fmt.Errorf("%w: %s.%s has named results or is async", ErrUnsupported, name, f.Name)
		}
		d.funcs = append(d.funcs, f.Name)
		d.sigs[f.Name] = sig
	}
	return d, nil
}

// MustDefine is Define for the WIT a binary embeds, which either reads or is a bug.
func MustDefine(fsys fs.FS, dir, name string) *Definition {
	d, err := Define(fsys, dir, name)
	if err != nil {
		panic(err)
	}
	return d
}

// Name is the interface's qualified name, "taubyte:secret/vault@0.1.0".
func (d *Definition) Name() string { return d.name }

// Funcs is every function the interface declares, in declaration order.
func (d *Definition) Funcs() []string { return slices.Clone(d.funcs) }

// Source is the WIT package the interface was read from, for an engine that reads WIT
// itself.
func (d *Definition) Source() (fs.FS, string) { return d.fsys, d.dir }

// Signature is a function's types as wazy describes them.
func (d *Definition) Signature(fn string) *wit.Signature { return d.sigs[fn] }

// Module is a Definition with its functions: what a binder attaches to an instance.
type Module struct {
	Definition *Definition
	Funcs      map[string]Func
}

// Implement is a module of d, refused unless funcs implements exactly what d declares.
func (d *Definition) Implement(funcs map[string]Func) (Module, error) {
	for _, name := range d.funcs {
		if funcs[name] == nil {
			return Module{}, fmt.Errorf("%w: %s.%s is not implemented", ErrIncomplete, d.name, name)
		}
	}
	for name := range funcs {
		if !slices.Contains(d.funcs, name) {
			return Module{}, fmt.Errorf("%w: %s declares no %s", ErrIncomplete, d.name, name)
		}
	}
	return Module{Definition: d, Funcs: funcs}, nil
}

// supported refuses, at Define, a type no binder converts, rather than at a call.
func supported(ref component.TypeRef, resolve component.Resolver) error {
	if ref.Primitive != "" {
		return nil
	}
	switch t := resolve(*ref.TypeIndex).(type) {
	case component.PrimitiveDesc, component.EnumDesc, component.FlagsDesc:
		return nil
	case component.ListDesc:
		return supported(t.Element, resolve)
	case component.OptionDesc:
		return supported(t.Element, resolve)
	case component.TupleDesc:
		for _, e := range t.Elements {
			if err := supported(e, resolve); err != nil {
				return err
			}
		}
		return nil
	case component.RecordDesc:
		for _, f := range t.Fields {
			if err := supported(f.Type, resolve); err != nil {
				return err
			}
		}
		return nil
	case component.VariantDesc:
		for _, c := range t.Cases {
			if c.Type != nil {
				if err := supported(*c.Type, resolve); err != nil {
					return err
				}
			}
		}
		return nil
	case component.ResultDesc:
		for _, arm := range []*component.TypeRef{t.Ok, t.Err} {
			if arm != nil {
				if err := supported(*arm, resolve); err != nil {
					return err
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrUnsupported, t.Kind())
	}
}
