package vm

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sync"

	wazyapi "github.com/samyfodil/wazy/api"
	"github.com/taubyte/tau/core/vm"
)

var _ vm.HostModule = &hostModule{}

var (
	moduleType = reflect.TypeOf((*vm.Module)(nil)).Elem()
	ctxType    = reflect.TypeOf((*context.Context)(nil)).Elem()
)

// reflectedSignature is the per-handler-type work derivable once: the wasm
// param/result value types, plus, for each Go param, how to fill it at call
// time (context, module, or a numeric decoded from the stack). Handlers are
// re-attached on every wasm cold start but their func types repeat, so this is
// cached process-wide.
//
// This is the legacy path: it keeps reflection at call time so the ~200
// hand-written W_ host methods work unchanged. Hot paths should instead use
// the reflection-free vm.HostModuleFunctionDefinition.Stack fast path.
type reflectedSignature struct {
	params  []vm.ValueType
	results []vm.ValueType
	// paramPlan[i] describes Go param i: kind==ctxParam / moduleParam, or a
	// numeric with its reflect.Type (for Convert) and stack slot.
	paramPlan []paramSlot
}

type paramSlot struct {
	kind    int // ctxParam | moduleParam | numericParam
	typ     reflect.Type
	valType vm.ValueType
	slot    int // stack index for numericParam
}

const (
	ctxParam = iota
	moduleParam
	numericParam
)

var reflectedSignatures sync.Map // reflect.Type -> *reflectedSignature

func kindToValueType(k reflect.Kind) (vm.ValueType, bool) {
	switch k {
	case reflect.Int32, reflect.Uint32:
		return vm.ValueTypeI32, true
	case reflect.Int64, reflect.Uint64:
		return vm.ValueTypeI64, true
	case reflect.Float32:
		return vm.ValueTypeF32, true
	case reflect.Float64:
		return vm.ValueTypeF64, true
	default:
		return 0, false
	}
}

func buildReflectedSignature(tp reflect.Type) (*reflectedSignature, error) {
	sig := &reflectedSignature{paramPlan: make([]paramSlot, tp.NumIn())}
	for i := 0; i < tp.NumIn(); i++ {
		in := tp.In(i)
		switch {
		case in == ctxType:
			sig.paramPlan[i] = paramSlot{kind: ctxParam}
		case in.Kind() == reflect.Interface && moduleType.AssignableTo(in):
			sig.paramPlan[i] = paramSlot{kind: moduleParam}
		default:
			vt, ok := kindToValueType(in.Kind())
			if !ok {
				return nil, fmt.Errorf("unsupported host function parameter %d of type %s", i, in)
			}
			sig.paramPlan[i] = paramSlot{kind: numericParam, typ: in, valType: vt, slot: len(sig.params)}
			sig.params = append(sig.params, vt)
		}
	}

	for i := 0; i < tp.NumOut(); i++ {
		vt, ok := kindToValueType(tp.Out(i).Kind())
		if !ok {
			return nil, fmt.Errorf("unsupported host function result %d of type %s", i, tp.Out(i))
		}
		sig.results = append(sig.results, vt)
	}

	return sig, nil
}

// decodeParam turns a stack slot into a reflect.Value assignable to the Go
// param type (Convert handles named types like errno.Error).
func decodeParam(s paramSlot, raw uint64) reflect.Value {
	var v reflect.Value
	switch s.valType {
	case vm.ValueTypeI32:
		if s.typ.Kind() == reflect.Int32 {
			v = reflect.ValueOf(int32(raw))
		} else {
			v = reflect.ValueOf(uint32(raw))
		}
	case vm.ValueTypeI64:
		if s.typ.Kind() == reflect.Int64 {
			v = reflect.ValueOf(int64(raw))
		} else {
			v = reflect.ValueOf(raw)
		}
	case vm.ValueTypeF32:
		v = reflect.ValueOf(math.Float32frombits(uint32(raw)))
	default: // F64
		v = reflect.ValueOf(math.Float64frombits(raw))
	}
	return v.Convert(s.typ)
}

func encodeResult(vt vm.ValueType, v reflect.Value) uint64 {
	switch vt {
	case vm.ValueTypeI32:
		if v.Kind() == reflect.Int32 {
			return wazyapi.EncodeI32(int32(v.Int()))
		}
		return wazyapi.EncodeU32(uint32(v.Uint()))
	case vm.ValueTypeI64:
		if v.Kind() == reflect.Int64 {
			return uint64(v.Int())
		}
		return v.Uint()
	case vm.ValueTypeF32:
		return wazyapi.EncodeF32(float32(v.Float()))
	default: // F64
		return wazyapi.EncodeF64(v.Float())
	}
}

// reflectedHandler wraps an arbitrary Go host func as a stack-based adapter.
// Reflection is done once (signature) and cached; each call reflects args/rets.
func reflectedHandler(handler vm.HostFunction) (functionDef, error) {
	tp := reflect.TypeOf(handler)
	if tp == nil || tp.Kind() != reflect.Func {
		return functionDef{}, fmt.Errorf("host function handler must be a func, got %T", handler)
	}

	var sig *reflectedSignature
	if cached, ok := reflectedSignatures.Load(tp); ok {
		sig = cached.(*reflectedSignature)
	} else {
		var err error
		if sig, err = buildReflectedSignature(tp); err != nil {
			return functionDef{}, err
		}
		reflectedSignatures.Store(tp, sig)
	}

	hv := reflect.ValueOf(handler)
	fn := func(ctx context.Context, mod wazyapi.Module, stack []uint64) {
		args := make([]reflect.Value, len(sig.paramPlan))
		for i, p := range sig.paramPlan {
			switch p.kind {
			case ctxParam:
				args[i] = reflect.ValueOf(ctx)
			case moduleParam:
				args[i] = reflect.ValueOf(mod)
			default:
				args[i] = decodeParam(p, stack[p.slot])
			}
		}
		rets := hv.Call(args)
		for i, vt := range sig.results {
			stack[i] = encodeResult(vt, rets[i])
		}
	}

	return functionDef{fn: fn, params: sig.params, results: sig.results}, nil
}

func (hm *hostModule) add(name string, fd functionDef) error {
	if _, exists := hm.functions[name]; exists {
		return fmt.Errorf("function `%s` @ `%s` already defined", name, hm.name)
	}
	hm.functions[name] = fd
	return nil
}

func (hm *hostModule) Functions(defs ...*vm.HostModuleFunctionDefinition) error {
	for _, def := range defs {
		if def == nil { // FIXME: we should not need this
			continue
		}

		// Reflection-free fast path: caller supplied the wasm signature and a
		// stack adapter. Register it directly, no reflection at call time.
		if def.Stack != nil {
			fd := functionDef{
				fn:      def.Stack, // vm.Module is api.Module: no wrapper needed
				params:  def.ParamTypes,
				results: def.ResultTypes,
			}
			if err := hm.add(def.Name, fd); err != nil {
				return err
			}
			continue
		}

		fd, err := reflectedHandler(def.Handler)
		if err != nil {
			return err
		}
		if err := hm.add(def.Name, fd); err != nil {
			return err
		}
	}
	return nil
}

func (hm *hostModule) memory(def *vm.HostModuleMemoryDefinition) error {
	if def != nil {
		if _, exists := hm.memories[def.Name]; exists {
			return fmt.Errorf("memory `%s` @ `%s` already defined", def.Name, hm.name)
		}

		hm.memories[def.Name] = memoryPages{
			min:   def.Pages.Min,
			max:   def.Pages.Max,
			maxed: def.Pages.Maxed,
		}
	}

	return nil
}

func (hm *hostModule) Memories(defs ...*vm.HostModuleMemoryDefinition) error {
	for _, def := range defs {
		if err := hm.memory(def); err != nil {
			return err
		}
	}

	return nil
}

func (hm *hostModule) global(def *vm.HostModuleGlobalDefinition) error {
	if def != nil {
		if _, exists := hm.globals[def.Name]; exists {
			return fmt.Errorf("global `%s` @ `%s` already defined", def.Name, hm.name)
		}

		hm.globals[def.Name] = def.Value
	}

	return nil
}

func (hm *hostModule) Globals(defs ...*vm.HostModuleGlobalDefinition) error {
	for _, def := range defs {
		if err := hm.global(def); err != nil {
			return err
		}
	}

	return nil
}

func (hm *hostModule) Compile() (vm.ModuleInstance, error) {
	wb := hm.runtime.runtime.NewHostModuleBuilder(hm.name)
	for name, def := range hm.functions {
		wb.NewFunctionBuilder().
			WithGoModuleFunction(wazyapi.GoModuleFunc(def.fn), def.params, def.results).
			Export(name)
	}

	if cm, err := wb.Instantiate(hm.ctx.Context()); err != nil {
		return nil, err
	} else {
		return &moduleInstance{
			module: cm,
			ctx:    hm.ctx.Context(),
		}, nil
	}
}
