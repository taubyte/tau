package wasm

import (
	"context"
	"fmt"
	"math/bits"
	"reflect"
	"slices"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke/hostmodule"
)

// attach is a host module as this tier's imports. Each call converts between the
// module's Values and wazy's by the types the module's WIT declares.
func attach(m hostmodule.Module) []component.Option {
	d := m.Definition
	out := make([]component.Option, 0, len(m.Funcs))
	for _, name := range d.Funcs() {
		sig := d.Signature(name)
		resolve := sig.Types.Resolver()
		params, result, fn := sig.Desc.Params, sig.Desc.Results.Unnamed, m.Funcs[name]
		where := d.Name() + "." + name

		host := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
			if len(args) != len(params) {
				return nil, fmt.Errorf("%s: %d arguments, want %d", where, len(args), len(params))
			}
			in := make([]hostmodule.Value, len(args))
			for i, a := range args {
				v, err := lift(params[i].Type, resolve, a)
				if err != nil {
					return nil, fmt.Errorf("%s: %s: %w", where, params[i].Name, err)
				}
				in[i] = v
			}

			answer, err := fn(ctx, in)
			if err != nil || result == nil {
				return nil, err
			}
			v, err := lower(*result, resolve, answer)
			if err != nil {
				return nil, fmt.Errorf("%s: result: %w", where, err)
			}
			return []component.Value{v}, nil
		}
		out = append(out, component.WithImportCustom(d.Name(), name, host, sig.Desc, resolve))
	}
	return out
}

// lift reads a wazy value as a module's Value.
func lift(ref component.TypeRef, resolve component.Resolver, v component.Value) (hostmodule.Value, error) {
	if ref.Primitive != "" {
		return v, nil
	}
	switch t := resolve(*ref.TypeIndex).(type) {
	case component.PrimitiveDesc:
		return v, nil
	case component.ListDesc:
		if t.Element.Primitive == "u8" {
			return v, nil
		}
		return liftEach(v, func(int) component.TypeRef { return t.Element }, resolve)
	case component.TupleDesc:
		return liftEach(v, func(i int) component.TypeRef { return t.Elements[i] }, resolve)
	case component.OptionDesc:
		if v == nil {
			return nil, nil
		}
		return lift(t.Element, resolve, v)
	case component.RecordDesc:
		fields, ok := v.([]component.Value)
		if !ok || len(fields) != len(t.Fields) {
			return nil, fmt.Errorf("a record is %T", v)
		}
		out := make(hostmodule.Record, len(fields))
		for i, f := range t.Fields {
			field, err := lift(f.Type, resolve, fields[i])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f.Name, err)
			}
			out[f.Name] = field
		}
		return out, nil
	case component.VariantDesc:
		vv, ok := v.(component.VariantValue)
		if !ok || int(vv.Disc) >= len(t.Cases) {
			return nil, fmt.Errorf("a variant is %T", v)
		}
		c := t.Cases[vv.Disc]
		out := hostmodule.Variant{Case: c.Name}
		if c.Type != nil {
			payload, err := lift(*c.Type, resolve, vv.Payload)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", c.Name, err)
			}
			out.Payload = payload
		}
		return out, nil
	case component.EnumDesc:
		i, ok := v.(uint32)
		if !ok || int(i) >= len(t.Cases) {
			return nil, fmt.Errorf("an enum is %v", v)
		}
		return t.Cases[i], nil
	case component.FlagsDesc:
		set, ok := v.(uint32)
		if !ok {
			return nil, fmt.Errorf("flags are %T", v)
		}
		var out []string
		for set != 0 {
			i := bits.TrailingZeros32(set)
			if i >= len(t.Names) {
				return nil, fmt.Errorf("flag %d of %d", i, len(t.Names))
			}
			out = append(out, t.Names[i])
			set &^= 1 << i
		}
		return out, nil
	case component.ResultDesc:
		rv, ok := v.(component.ResultValue)
		if !ok {
			return nil, fmt.Errorf("a result is %T", v)
		}
		arm := t.Ok
		if rv.IsErr {
			arm = t.Err
		}
		out := hostmodule.Result{Err: rv.IsErr}
		if arm != nil {
			payload, err := lift(*arm, resolve, rv.Payload)
			if err != nil {
				return nil, err
			}
			out.Payload = payload
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: %s", hostmodule.ErrUnsupported, t.Kind())
	}
}

// liftEach is a list or tuple: a []Value, or the typed slice wazy lifts a list of a
// fixed-width primitive to.
func liftEach(v component.Value, elem func(int) component.TypeRef, resolve component.Resolver) ([]hostmodule.Value, error) {
	if items, ok := v.([]component.Value); ok {
		out := make([]hostmodule.Value, len(items))
		for i, item := range items {
			lifted, err := lift(elem(i), resolve, item)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			out[i] = lifted
		}
		return out, nil
	}
	s := reflect.ValueOf(v)
	if s.Kind() != reflect.Slice {
		return nil, fmt.Errorf("a list is %T", v)
	}
	out := make([]hostmodule.Value, s.Len())
	for i := range out {
		out[i] = s.Index(i).Interface()
	}
	return out, nil
}

// lower writes a module's Value as wazy's.
func lower(ref component.TypeRef, resolve component.Resolver, v hostmodule.Value) (component.Value, error) {
	if ref.Primitive != "" {
		return primitive(ref.Primitive, v)
	}
	switch t := resolve(*ref.TypeIndex).(type) {
	case component.PrimitiveDesc:
		return primitive(t.Prim, v)
	case component.ListDesc:
		if t.Element.Primitive == "u8" {
			if b, ok := v.([]byte); ok {
				return b, nil
			}
		}
		return lowerEach(v, func(int) component.TypeRef { return t.Element }, -1, resolve)
	case component.TupleDesc:
		return lowerEach(v, func(i int) component.TypeRef { return t.Elements[i] }, len(t.Elements), resolve)
	case component.OptionDesc:
		if v == nil {
			return nil, nil
		}
		return lower(t.Element, resolve, v)
	case component.RecordDesc:
		r, ok := v.(hostmodule.Record)
		if !ok {
			return nil, fmt.Errorf("a record is a hostmodule.Record, not %T", v)
		}
		out := make([]component.Value, len(t.Fields))
		for i, f := range t.Fields {
			field, ok := r[f.Name]
			if !ok {
				return nil, fmt.Errorf("the record has no %s", f.Name)
			}
			lowered, err := lower(f.Type, resolve, field)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f.Name, err)
			}
			out[i] = lowered
		}
		return out, nil
	case component.VariantDesc:
		vv, ok := v.(hostmodule.Variant)
		if !ok {
			return nil, fmt.Errorf("a variant is a hostmodule.Variant, not %T", v)
		}
		disc := slices.IndexFunc(t.Cases, func(c component.VariantCase) bool { return c.Name == vv.Case })
		if disc < 0 {
			return nil, fmt.Errorf("the variant has no case %q", vv.Case)
		}
		out := component.VariantValue{Disc: uint32(disc)}
		if c := t.Cases[disc]; c.Type != nil {
			payload, err := lower(*c.Type, resolve, vv.Payload)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", vv.Case, err)
			}
			out.Payload = payload
		}
		return out, nil
	case component.EnumDesc:
		name, ok := v.(string)
		i := slices.Index(t.Cases, name)
		if !ok || i < 0 {
			return nil, fmt.Errorf("the enum has no case %v", v)
		}
		return uint32(i), nil
	case component.FlagsDesc:
		names, ok := v.([]string)
		if !ok {
			return nil, fmt.Errorf("flags are a []string, not %T", v)
		}
		var set uint32
		for _, name := range names {
			i := slices.Index(t.Names, name)
			if i < 0 {
				return nil, fmt.Errorf("no flag %q", name)
			}
			set |= 1 << i
		}
		return set, nil
	case component.ResultDesc:
		r, ok := v.(hostmodule.Result)
		if !ok {
			return nil, fmt.Errorf("a result is a hostmodule.Result, not %T", v)
		}
		arm := t.Ok
		if r.Err {
			arm = t.Err
		}
		out := component.ResultValue{IsErr: r.Err}
		if arm != nil {
			payload, err := lower(*arm, resolve, r.Payload)
			if err != nil {
				return nil, err
			}
			out.Payload = payload
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: %s", hostmodule.ErrUnsupported, t.Kind())
	}
}

// lowerEach is a list, or a tuple of exactly want elements.
func lowerEach(v hostmodule.Value, elem func(int) component.TypeRef, want int, resolve component.Resolver) ([]component.Value, error) {
	items, ok := v.([]hostmodule.Value)
	if !ok {
		return nil, fmt.Errorf("a list or tuple is a []hostmodule.Value, not %T", v)
	}
	if want >= 0 && len(items) != want {
		return nil, fmt.Errorf("a tuple of %d, want %d", len(items), want)
	}
	out := make([]component.Value, len(items))
	for i, item := range items {
		lowered, err := lower(elem(i), resolve, item)
		if err != nil {
			return nil, fmt.Errorf("[%d]: %w", i, err)
		}
		out[i] = lowered
	}
	return out, nil
}

// primitive refuses a Go value that is not the primitive's own shape, naming both.
func primitive(prim string, v hostmodule.Value) (component.Value, error) {
	var ok bool
	switch prim {
	case "bool":
		_, ok = v.(bool)
	case "s8", "s16", "s32":
		_, ok = v.(int32)
	case "u8", "u16", "u32":
		_, ok = v.(uint32)
	case "s64":
		_, ok = v.(int64)
	case "u64":
		_, ok = v.(uint64)
	case "f32":
		_, ok = v.(float32)
	case "f64":
		_, ok = v.(float64)
	case "char":
		_, ok = v.(rune)
	case "string":
		_, ok = v.(string)
	}
	if !ok {
		return nil, fmt.Errorf("a %s is not a %T", prim, v)
	}
	return v, nil
}
