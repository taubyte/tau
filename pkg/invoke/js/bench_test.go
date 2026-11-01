package js_test

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"warpjs/engine"

	"github.com/taubyte/tau/pkg/invoke"
	js "github.com/taubyte/tau/pkg/invoke/js"
)

// The benchmarks measure four things apart:
//
//	BenchmarkEngine  JS doing CPU work, called warm from Go: the engine itself
//	BenchmarkCall    an empty function called from Go: what one call costs
//	BenchmarkServe   Serve on a warm instance: the tier's request path
//	BenchmarkWarmup  a process's first realm, and its first native code
//
// The first three warm one instance until it stops getting faster before timing.

const workers = `
let calls = 0;
export default {
  async fetch(request) {
    calls++;
    const body = await request.text();
    return new Response(request.method + " " + body.length + " " + calls, { headers: { "x-calls": String(calls) } });
  },
};
`

// bundle is a module of about 150 KiB, the size the warpjs survey measured:
// stage 1 compiles it from source in every new realm.
func bundle() string {
	var b strings.Builder
	for n := range 2500 {
		fmt.Fprintf(&b, "export function f%d(a, b) { const x = a * %d + b; return x > 0 ? x - 1 : x + 1; }\n", n, n)
	}
	b.WriteString(workers)
	return b.String()
}

func modes() []js.Mode {
	out := []js.Mode{js.ModeBytecode}
	if engine.NativeSupported() {
		out = append(out, js.ModeNative, js.ModeJIT)
	}
	return out
}

func BenchmarkColdStart(b *testing.B) {
	for _, size := range []struct {
		name, src string
	}{{"small", workers}, {"150KiB", bundle()}} {
		for _, mode := range modes() {
			b.Run(size.name+"/"+string(mode), func(b *testing.B) {
				f := newFixture(b, size.src, js.WithMode(mode))
				req := get("/")
				b.ResetTimer()
				for range b.N {
					inst, err := f.tier.Start(context.Background(), f.spec)
					if err != nil {
						b.Fatal(err)
					}
					if _, err := inst.Serve(context.Background(), req); err != nil {
						b.Fatal(err)
					}
					inst.Close()
				}
			})
		}
	}
}

// minWarm is the least the first instance is warmed: a process's first native
// realm compiles for most of a second.
const minWarm = time.Second

// warm calls f in batches until a batch is within a tenth of the one before
// it, and for at least least. It stops at f's first error.
func warm(f func() error, least time.Duration) error {
	const batch = 64
	began := time.Now()
	var last time.Duration
	for n := 0; ; n++ {
		t0 := time.Now()
		for range batch {
			if err := f(); err != nil {
				return err
			}
		}
		took := time.Since(t0)
		if n > 0 && time.Since(began) >= least && took*10 >= last*9 {
			return nil
		}
		last = took
	}
}

// engineFault is warpjs failing inside a call (a collector panic, or a runtime
// error from a corrupted arena), which the tier turns into a retired instance. The
// benchmark replaces the instance with the timer stopped and reports the count as
// "replaced".
func engineFault(b *testing.B, err error) bool {
	msg := err.Error()
	if !strings.Contains(msg, "Sweep found") && !strings.Contains(msg, "runtime error") && !strings.Contains(msg, "the engine panicked") {
		return false
	}
	if !faults[msg] {
		faults[msg] = true
		b.Logf("replacing the instance: %v", err)
	}
	return true
}

var faults = map[string]bool{}

// kernels is CPU work for the engine to run, beside a handler that does
// nothing, so a tier realm can be called into directly.
const kernels = `
export function empty() {}

export function loop(n) {
  let s = 0;
  for (let i = 0; i < n; i++) s = (s + i * i) % 1000003;
  return s;
}

export function strings(n) {
  let s = "";
  for (let i = 0; i < n; i++) s += String.fromCharCode(97 + (i % 26));
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
  return h + s.split("a").length + s.indexOf("zab");
}

export function arrays(n) {
  const a = [];
  for (let i = 0; i < n; i++) a.push((i * 7919) % n);
  let s = 0;
  for (const x of a) s += x;
  return s + a.map((x) => x * 2).filter((x) => x % 3 === 0).length;
}

export function closures(n) {
  let s = 0;
  for (let i = 0; i < n; i++) {
    const add = (x) => x + i;
    s = add(s) % 1000003;
  }
  return s;
}

// What a request builds, one kind at a time, beside the shapes it could take.
class Base { #a = null; #b = false; constructor(v) { this.#a = v; } }
class Derived extends Base { #c; #d; #e; constructor(v) { super(v); this.#c = v; this.#d = v; this.#e = v; } }
class Flat { #s; constructor(v) { this.#s = { a: v, b: false, c: v, d: v, e: v }; } }
const making = (make) => (n) => {
  let x;
  for (let i = 0; i < n; i++) x = make(i);
  return x !== undefined;
};

globalThis.kernels = {
  empty, loop, strings, arrays, closures,
  literal: making((i) => ({ a: i, b: false, c: i, d: i, e: i })),
  flat: making((i) => new Flat(i)),
  derived: making((i) => new Derived(i)),
  headers: making(() => new Headers()),
  response: making(() => new Response()),
  request: making(() => new Request("http://workload.invalid/")),
  promise: making((i) => Promise.resolve(i).then(Number).catch(Number)),
};

export default { fetch() { return new Response(); } };
`

// The size each kernel is called with: a bytecode call takes long enough
// that the call around it does not count.
const (
	loopN     = 20000
	stringsN  = 4000
	arraysN   = 4000
	closuresN = 4000
)

// realm is one tier realm whose kernel is called straight from Go.
type realm struct {
	f    *fixture
	inst invoke.Instance
	rt   *engine.Runtime
	fn   engine.Value
	arg  engine.Value

	// run is where each call runs: nil calls with no bounds at all.
	run context.Context

	kernel   string
	n        int
	replaced int
}

func newRealm(b *testing.B, mode js.Mode, kernel string, n int, run context.Context) *realm {
	r := &realm{f: newFixture(b, kernels, js.WithMode(mode)), run: run, kernel: kernel, n: n}
	r.start(b)
	return r
}

func (r *realm) start(b *testing.B) {
	for {
		inst, err := r.f.tier.Start(context.Background(), r.f.spec)
		if err != nil {
			b.Fatal(err)
		}
		r.inst, r.rt = inst, js.Realm(inst)
		r.fn = r.rt.Get("kernels").Get(r.kernel)
		r.arg = r.rt.ToValue(r.n)
		least := minWarm
		if r.replaced > 0 {
			least = 0
		}
		err = warm(r.call, least)
		if err == nil {
			return
		}
		r.replace(b, err)
	}
}

func (r *realm) replace(b *testing.B, err error) {
	if !engineFault(b, err) {
		b.Fatal(err)
	}
	r.inst.Close()
	r.replaced++
}

func (r *realm) call() (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("the engine panicked: %v", p)
		}
	}()
	if r.run == nil {
		_, err = r.rt.Call(r.fn, engine.Value{}, r.arg)
		return err
	}
	return r.rt.RunContext(r.run, func() error {
		_, err := r.rt.Call(r.fn, engine.Value{}, r.arg)
		return err
	})
}

func (r *realm) time(b *testing.B) {
	before := r.replaced
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := r.call(); err != nil {
			b.StopTimer()
			r.replace(b, err)
			r.start(b)
			b.StartTimer()
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(r.replaced-before), "replaced")
}

func BenchmarkEngine(b *testing.B) {
	for _, k := range []struct {
		name string
		n    int
	}{{"loop", loopN}, {"strings", stringsN}, {"arrays", arraysN}, {"closures", closuresN}} {
		for _, mode := range modes() {
			var r *realm
			b.Run(k.name+"/"+string(mode), func(b *testing.B) {
				if r == nil {
					r = newRealm(b, mode, k.name, k.n, nil)
				}
				r.time(b)
			})
			if r != nil {
				r.inst.Close()
			}
		}
	}
}

func BenchmarkCall(b *testing.B) {
	bounded, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	for _, c := range []struct {
		name string
		run  context.Context
	}{{"empty", nil}, {"empty-deadline", bounded}} {
		for _, mode := range modes() {
			var r *realm
			b.Run(c.name+"/"+string(mode), func(b *testing.B) {
				if r == nil {
					r = newRealm(b, mode, "empty", 0, c.run)
				}
				r.time(b)
			})
			if r != nil {
				r.inst.Close()
			}
		}
	}
}

// objectsN is how many objects one call builds.
const objectsN = 100

func BenchmarkObjects(b *testing.B) {
	for _, kind := range []string{"literal", "flat", "derived", "headers", "response", "request", "promise"} {
		for _, mode := range modes() {
			var r *realm
			b.Run(kind+"/"+string(mode), func(b *testing.B) {
				if r == nil {
					r = newRealm(b, mode, kind, objectsN, nil)
				}
				r.time(b)
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*objectsN), "ns/object")
			})
			if r != nil {
				r.inst.Close()
			}
		}
	}
}

const (
	empty = `export default { fetch() { return new Response(); } };`

	echoBytes = `export default { async fetch(request) { return new Response(await request.arrayBuffer()); } };`

	echoText = `export default { async fetch(request) { return new Response(await request.text()); } };`
)

// server is one instance served warm.
type server struct {
	f    *fixture
	inst invoke.Instance
	req  *invoke.Request

	// echo is whether the answer must carry the request's body.
	echo     bool
	replaced int
}

func (s *server) start(b *testing.B) {
	for {
		inst, err := s.f.tier.Start(context.Background(), s.f.spec)
		if err != nil {
			b.Fatal(err)
		}
		s.inst = inst
		resp, err := inst.Serve(context.Background(), s.req)
		if err == nil && (resp.Status != 200 || (s.echo && !bytes.Equal(resp.Body, s.req.Body))) {
			b.Fatalf("answered %d with %d bytes; want 200 and the %d sent: %s", resp.Status, len(resp.Body), len(s.req.Body), s.f.logs.err())
		}
		least := minWarm
		if s.replaced > 0 {
			least = 0
		}
		if err == nil {
			err = warm(s.serve, least)
		}
		if err == nil {
			return
		}
		s.replace(b, err)
	}
}

func (s *server) replace(b *testing.B, err error) {
	if !engineFault(b, err) {
		b.Fatal(err)
	}
	s.inst.Close()
	s.replaced++
}

func (s *server) serve() error {
	_, err := s.inst.Serve(context.Background(), s.req)
	return err
}

func BenchmarkServe(b *testing.B) {
	body := func(n int) []byte {
		out := make([]byte, n)
		for i := range out {
			out[i] = 'a' + byte(i%26)
		}
		return out
	}
	for _, c := range []struct {
		name, src string
		req       *invoke.Request
		echo      bool
	}{
		{"empty", empty, get("/"), false},
		{"workers", workers, get("/"), false},
		{"bytes-1KiB", echoBytes, &invoke.Request{Method: "POST", Path: "/", Body: body(1 << 10)}, true},
		{"bytes-128KiB", echoBytes, &invoke.Request{Method: "POST", Path: "/", Body: body(128 << 10)}, true},
		{"text-1KiB", echoText, &invoke.Request{Method: "POST", Path: "/", Body: body(1 << 10)}, true},
		{"text-128KiB", echoText, &invoke.Request{Method: "POST", Path: "/", Body: body(128 << 10)}, true},
	} {
		for _, mode := range modes() {
			var s *server
			b.Run(c.name+"/"+string(mode), func(b *testing.B) {
				if s == nil {
					s = &server{f: newFixture(b, c.src, js.WithMode(mode)), req: c.req, echo: c.echo}
					s.start(b)
				}
				before := s.replaced
				b.SetBytes(int64(len(c.req.Body)))
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if err := s.serve(); err != nil {
						b.StopTimer()
						s.replace(b, err)
						s.start(b)
						b.StartTimer()
					}
				}
				b.StopTimer()
				b.ReportMetric(float64(s.replaced-before), "replaced")
			})
			if s != nil {
				s.inst.Close()
			}
		}
	}
}

// onceOnly is why BenchmarkWarmup refuses to repeat: the native code cache is
// process-wide, so only a process's first realm shows the warm-up.
const onceOnly = "BenchmarkWarmup measures a process's first realm: run one sub-benchmark per process, with -benchtime=1x -count=1"

func BenchmarkWarmup(b *testing.B) {
	for _, mode := range modes() {
		b.Run("realms/"+string(mode), func(b *testing.B) {
			if b.N != 1 {
				b.Skip(onceOnly)
			}
			f := newFixture(b, workers, js.WithMode(mode))
			req := get("/")
			for k := range 3 {
				t0 := time.Now()
				inst, err := f.tier.Start(context.Background(), f.spec)
				if err != nil {
					b.Fatal(err)
				}
				started := time.Since(t0)
				took := make([]time.Duration, 2000)
				for i := range took {
					t := time.Now()
					if _, err := inst.Serve(context.Background(), req); err != nil {
						b.Fatalf("realm %d, request %d: %v", k, i, err)
					}
					took[i] = time.Since(t)
				}
				inst.Close()
				p := fmt.Sprintf("r%d-", k)
				b.ReportMetric(ms(started), p+"start-ms")
				b.ReportMetric(us(took[0]), p+"call1-us")
				b.ReportMetric(ms(sum(took[:1000])), p+"calls1-1000-ms")
				b.ReportMetric(us(median(took[1000:])), p+"steady-us")
			}
		})
		b.Run("native/"+string(mode), func(b *testing.B) {
			if b.N != 1 {
				b.Skip(onceOnly)
			}
			f := newFixture(b, kernels, js.WithMode(mode))
			inst := f.start()
			r := &realm{inst: inst, rt: js.Realm(inst)}
			r.fn, r.arg = r.rt.Get("kernels").Get("loop"), r.rt.ToValue(loopN)
			took := make([]time.Duration, 500)
			for i := range took {
				t := time.Now()
				if err := r.call(); err != nil {
					b.Fatalf("call %d: %v", i, err)
				}
				took[i] = time.Since(t)
			}
			steady := median(took[len(took)-100:])
			at := 0
			for at < len(took) && took[at]*2 > steady*3 {
				at++
			}
			b.ReportMetric(us(took[0]), "call1-us")
			b.ReportMetric(float64(at+1), "steady-from-call")
			b.ReportMetric(ms(sum(took[:at])), "before-steady-ms")
			b.ReportMetric(us(steady), "steady-us")
		})
	}
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func us(d time.Duration) float64 { return float64(d) / float64(time.Microsecond) }

func sum(ds []time.Duration) time.Duration {
	var t time.Duration
	for _, d := range ds {
		t += d
	}
	return t
}

func median(ds []time.Duration) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[len(s)/2]
}
