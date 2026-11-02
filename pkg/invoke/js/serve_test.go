package js_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"warpjs/engine"

	"github.com/taubyte/tau/pkg/invoke"
	js "github.com/taubyte/tau/pkg/invoke/js"
)

const counter = `
let calls = 0;
export default {
  fetch(request) {
    calls++;
    if (request.url.endsWith("/boom")) throw new Error("kaboom");
    if (request.url.endsWith("/reject")) return Promise.reject(new TypeError("refused later"));
    if (request.url.endsWith("/nothing")) return "not a response";
    if (request.url.endsWith("/spin")) for (;;) {}
    if (request.url.endsWith("/spin-then")) return Promise.resolve().then(() => { for (;;) {} });
    if (request.url.endsWith("/hold")) return new Promise(() => {});
    return new Response(String(calls));
  },
};
`

func TestAColdStartThenWarmCallsKeepModuleState(t *testing.T) {
	f := newFixture(t, counter)
	inst := f.start()
	for want := 1; want <= 3; want++ {
		resp := serve(t, inst, get("/"))
		require.Equal(t, 200, resp.Status)
		require.Equal(t, []byte(string(rune('0'+want))), resp.Body)
	}

	p := f.pool()
	for _, want := range []string{"1", "2"} {
		resp, err := p.Do(t.Context(), f.spec, get("/"))
		require.NoError(t, err)
		require.Equal(t, want, string(resp.Body))
	}
	require.Equal(t, 1, p.Warm(f.spec))
}

func TestOneRequestAtATimePerInstance(t *testing.T) {
	f := newFixture(t, `
let active = 0, most = 0;
export default {
  async fetch() {
    active++;
    most = Math.max(most, active);
    await later(100);
    active--;
    return new Response(String(most));
  },
};
`)
	inst := f.start()

	var wg sync.WaitGroup
	bodies := make([]string, 2)
	began := time.Now()
	for n := range bodies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := inst.Serve(context.Background(), get("/"))
			require.NoError(t, err)
			bodies[n] = string(resp.Body)
		}()
	}
	wg.Wait()
	require.Equal(t, []string{"1", "1"}, bodies, "neither request saw the other inside the realm")
	require.GreaterOrEqual(t, time.Since(began), 200*time.Millisecond, "the second waited for the first")
}

func TestAWaitingRequestGivesUpWithItsCaller(t *testing.T) {
	f := newFixture(t, `export default { async fetch() { await later(300); return new Response("slow"); } };`)
	inst := f.start()

	go inst.Serve(context.Background(), get("/"))
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	began := time.Now()
	_, err := inst.Serve(ctx, get("/"))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(began), 200*time.Millisecond)
}

func TestTheRequestRoundTrips(t *testing.T) {
	f := newFixture(t, `
export default {
  async fetch(request) {
    const body = await request.text();
    const headers = new Headers({
      "x-method": request.method,
      "x-url": request.url,
      "x-thing": request.headers.get("X-Thing") ?? "none",
    });
    headers.append("set-cookie", "a=1");
    headers.append("set-cookie", "b=2");
    return new Response(body + "|" + [...request.headers.keys()].join(","), { status: 201, headers });
  },
};
`)
	inst := f.start()

	resp := serve(t, inst, &invoke.Request{
		Method:  "PUT",
		Path:    "/echo?x=1",
		Headers: map[string][]string{"X-Thing": {"a", "b"}, "Content-Type": {"text/plain"}},
		Body:    []byte("héllo 😀"),
	})
	require.Equal(t, 201, resp.Status)
	require.Equal(t, "héllo 😀|content-type,x-thing", string(resp.Body))
	require.Equal(t, map[string][]string{
		"X-Method":     {"PUT"},
		"X-Url":        {"http://workload.invalid/echo?x=1"},
		"X-Thing":      {"a, b"},
		"Set-Cookie":   {"a=1", "b=2"},
		"Content-Type": {"text/plain;charset=UTF-8"},
	}, resp.Headers)

	// A non-HTTP invocation arrives as the wasm tier's standard world has it.
	resp = serve(t, inst, nil)
	require.Equal(t, "|", string(resp.Body))
	require.Equal(t, []string{"POST"}, resp.Headers["X-Method"])
	require.Equal(t, []string{"http://workload.invalid/"}, resp.Headers["X-Url"])

	resp = serve(t, inst, &invoke.Request{Method: "GET", Path: "relative"})
	require.Equal(t, []string{"http://workload.invalid/relative"}, resp.Headers["X-Url"])
}

func TestARequestBodyReadsTheSameWhenever(t *testing.T) {
	f := newFixture(t, `
let late = "";
let kept = null;
export default {
  async fetch(request, env, ctx) {
    const path = request.url.slice("http://workload.invalid".length);
    if (path === "/late") {
      ctx.waitUntil(later(5).then(async () => { late = await request.text(); }));
      return new Response("later");
    }
    if (path === "/late-read") return new Response(late);
    if (path === "/keep") { kept = request; return new Response("kept"); }
    if (path === "/kept") return new Response(await kept.text());
    if (path === "/clone") {
      const copy = request.clone();
      return Response.json([await request.text(), await copy.text()]);
    }
    if (path === "/json") return Response.json(await request.json());
    return new Response(await request.text());
  },
};
`)
	inst := f.start()
	body := []byte("\xef\xbb\xbfh\xffi \xe2\x82\xac")
	want := "h\ufffdi \u20ac"
	post := func(path string) *invoke.Request { return &invoke.Request{Method: "POST", Path: path, Body: body} }

	require.Equal(t, want, string(serve(t, inst, post("/now")).Body))
	require.Equal(t, "later", string(serve(t, inst, post("/late")).Body))
	require.Equal(t, want, string(serve(t, inst, get("/late-read")).Body))
	require.Equal(t, "kept", string(serve(t, inst, post("/keep")).Body))
	require.Equal(t, want, string(serve(t, inst, get("/kept")).Body))
	require.JSONEq(t, `["`+want+`","`+want+`"]`, string(serve(t, inst, post("/clone")).Body))
	resp := serve(t, inst, &invoke.Request{Method: "POST", Path: "/json", Body: []byte(`{"a":[1,"\u00e9"]}`)})
	require.JSONEq(t, `{"a":[1,"\u00e9"]}`, string(resp.Body))
}

func TestANon200StatusIsKept(t *testing.T) {
	f := newFixture(t, `
export default {
  fetch(request) {
    if (request.url.endsWith("/gone")) return new Response("gone", { status: 404, statusText: "Not Found" });
    if (request.url.endsWith("/json")) return Response.json({ tea: true }, { status: 418 });
    if (request.url.endsWith("/move")) return Response.redirect("https://elsewhere.example/", 308);
    return new Response(null, { status: 204 });
  },
};
`)
	inst := f.start()

	resp := serve(t, inst, get("/gone"))
	require.Equal(t, 404, resp.Status)
	require.Equal(t, "gone", string(resp.Body))

	resp = serve(t, inst, get("/json"))
	require.Equal(t, 418, resp.Status)
	require.Equal(t, `{"tea":true}`, string(resp.Body))
	require.Equal(t, []string{"application/json"}, resp.Headers["Content-Type"])

	resp = serve(t, inst, get("/move"))
	require.Equal(t, 308, resp.Status)
	require.Equal(t, []string{"https://elsewhere.example/"}, resp.Headers["Location"])

	resp = serve(t, inst, get("/empty"))
	require.Equal(t, 204, resp.Status)
	require.Nil(t, resp.Body)
	require.Nil(t, resp.Headers)
}

func TestAThrownErrorIsA500AndTheInstanceLivesOn(t *testing.T) {
	f := newFixture(t, counter)
	inst := f.start()

	for _, path := range []string{"/boom", "/reject", "/nothing"} {
		resp := serve(t, inst, get(path))
		require.Equal(t, 500, resp.Status, path)
		require.Equal(t, "Internal Server Error", string(resp.Body))
		require.Equal(t, []string{"text/plain;charset=UTF-8"}, resp.Headers["Content-Type"])
	}
	require.Contains(t, f.logs.err(), "Uncaught Error: kaboom")
	require.Contains(t, f.logs.err(), "worker.js", "with the stack")
	require.Contains(t, f.logs.err(), "Uncaught TypeError: refused later")
	require.Contains(t, f.logs.err(), "did not answer with a Response")

	resp := serve(t, inst, get("/"))
	require.Equal(t, "4", string(resp.Body), "the same realm, its state intact")

	p := f.pool()
	resp, err := p.Do(t.Context(), f.spec, get("/boom"))
	require.NoError(t, err)
	require.Equal(t, 500, resp.Status)
	require.Equal(t, 1, p.Warm(f.spec), "and the pool keeps it")
}

func TestAnEndlessLoopHitsTheDeadlineAndRetires(t *testing.T) {
	for _, path := range []string{"/spin", "/spin-then", "/hold"} {
		t.Run(path, func(t *testing.T) {
			f := newFixture(t, counter)
			inst := f.start()

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			began := time.Now()
			_, err := inst.Serve(ctx, get(path))
			require.ErrorIs(t, err, js.ErrDeadline)
			require.Less(t, time.Since(began), time.Second)

			_, err = inst.Serve(context.Background(), get("/"))
			require.ErrorIs(t, err, invoke.ErrNotServable, "a realm stopped mid-call is not reused")
			require.ErrorIs(t, err, js.ErrDeadline, "and says why")

			p := f.pool()
			resp, err := p.Do(t.Context(), f.spec, get("/"))
			require.NoError(t, err)
			require.Equal(t, "1", string(resp.Body))
			require.Equal(t, 1, p.Warm(f.spec))

			ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			_, err = p.Do(ctx, f.spec, get(path))
			require.ErrorIs(t, err, js.ErrDeadline)
			require.Zero(t, p.Warm(f.spec), "the warm realm it stopped is retired")
			resp, err = p.Do(t.Context(), f.spec, get("/"))
			require.NoError(t, err)
			require.Equal(t, "1", string(resp.Body), "the next request gets a fresh realm")
		})
	}
}

func TestTheSpecsTimeoutBoundsADirectCall(t *testing.T) {
	f := newFixture(t, counter)
	f.spec.Timeout = 100 * time.Millisecond
	inst := f.start()
	_, err := inst.Serve(context.Background(), get("/spin"))
	require.ErrorIs(t, err, js.ErrDeadline)
}

func TestACancelledCallerStopsTheWait(t *testing.T) {
	f := newFixture(t, `export default { async fetch() { await later(10000); return new Response("late"); } };`)
	inst := f.start()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	began := time.Now()
	_, err := inst.Serve(ctx, get("/"))
	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, time.Since(began), time.Second)

	_, err = inst.Serve(context.Background(), get("/"))
	require.ErrorIs(t, err, invoke.ErrNotServable)
}

const hog = `
export default {
  fetch(request) {
    if (request.url.endsWith("/small")) return new Response("fine");
    const keep = [];
    for (;;) keep.push("x".repeat(4096) + keep.length);
  },
};
`

func TestAMemoryBlowUpIsTypedAndRetires(t *testing.T) {
	f := newFixture(t, hog)
	f.spec.Memory = 16 << 20
	inst := f.start()
	require.Equal(t, "fine", string(serve(t, inst, get("/small")).Body))

	_, err := inst.Serve(context.Background(), get("/hog"))
	require.ErrorIs(t, err, js.ErrOutOfMemory)
	require.ErrorIs(t, err, engine.ErrOutOfMemory)

	_, err = inst.Serve(context.Background(), get("/small"))
	require.ErrorIs(t, err, invoke.ErrNotServable)

	p := f.pool()
	_, err = p.Do(t.Context(), f.spec, get("/hog"))
	require.ErrorIs(t, err, js.ErrOutOfMemory)
	require.Zero(t, p.Warm(f.spec))
}

func TestARequestTooBigForTheArenaIsRefused(t *testing.T) {
	f := newFixture(t, hog)
	f.spec.Memory = 16 << 20
	inst := f.start()

	_, err := inst.Serve(context.Background(), &invoke.Request{Method: "POST", Path: "/small", Body: make([]byte, 32<<20)})
	require.ErrorIs(t, err, js.ErrOutOfMemory)
	require.ErrorContains(t, err, "the request does not fit")
	_, err = inst.Serve(context.Background(), get("/small"))
	require.ErrorIs(t, err, invoke.ErrNotServable)
}

const waiter = `
let finished = 0;
export default {
  fetch(request, env, ctx) {
    if (request.url.endsWith("/later")) {
      ctx.waitUntil(later(200).then(() => { finished++; }));
      return new Response("answered");
    }
    if (request.url.endsWith("/chain")) {
      ctx.waitUntil(later(50).then(() => { ctx.waitUntil(later(50).then(() => { finished += 10; })); }));
      return new Response("answered");
    }
    if (request.url.endsWith("/fail")) {
      ctx.waitUntil(later(20).then(() => { throw new Error("background broke"); }));
      return new Response("answered");
    }
    if (request.url.endsWith("/spin")) {
      ctx.waitUntil(later(20).then(() => { for (;;) {} }));
      return new Response("answered");
    }
    if (request.url.endsWith("/forever")) {
      ctx.waitUntil(new Promise(() => {}));
      return new Response("answered");
    }
    if (request.url.endsWith("/done")) {
      ctx.waitUntil(Promise.resolve(1));
      ctx.passThroughOnException();
      return new Response("answered");
    }
    return new Response(String(finished));
  },
};
`

func TestWaitUntilAnswersAtOnceAndHoldsTheInstance(t *testing.T) {
	f := newFixture(t, waiter)
	p := f.pool(invoke.WithMaxInstances(1))

	// The clock starts on a warm instance: a cold start is not the work the
	// answer must not wait for, and under -race it alone outlasts the bound.
	resp, err := p.Do(t.Context(), f.spec, get("/count"))
	require.NoError(t, err)
	require.Equal(t, "0", string(resp.Body))

	began := time.Now()
	resp, err = p.Do(t.Context(), f.spec, get("/later"))
	require.NoError(t, err)
	require.Equal(t, "answered", string(resp.Body))
	require.Less(t, time.Since(began), 150*time.Millisecond, "the answer did not wait for the work")

	resp, err = p.Do(t.Context(), f.spec, get("/count"))
	require.NoError(t, err)
	require.Equal(t, "1", string(resp.Body), "the next request ran after the work settled")
	require.GreaterOrEqual(t, time.Since(began), 200*time.Millisecond)

	resp, err = p.Do(t.Context(), f.spec, get("/chain"))
	require.NoError(t, err)
	require.Equal(t, "answered", string(resp.Body))
	resp, err = p.Do(t.Context(), f.spec, get("/count"))
	require.NoError(t, err)
	require.Equal(t, "11", string(resp.Body), "work handed to waitUntil by earlier work is waited for too")

	resp, err = p.Do(t.Context(), f.spec, get("/done"))
	require.NoError(t, err)
	require.Equal(t, "answered", string(resp.Body), "work already settled holds nothing")
	require.Equal(t, 1, p.Warm(f.spec))
}

func TestWaitUntilWithoutAPoolFinishesFirst(t *testing.T) {
	f := newFixture(t, waiter)
	inst := f.start()

	began := time.Now()
	require.Equal(t, "answered", string(serve(t, inst, get("/later")).Body))
	require.GreaterOrEqual(t, time.Since(began), 200*time.Millisecond)
	require.Equal(t, "1", string(serve(t, inst, get("/count")).Body))

	require.Equal(t, "answered", string(serve(t, inst, get("/fail")).Body))
	require.Contains(t, f.logs.err(), "Uncaught Error: background broke", "a failure in the work is the author's to read")
	require.Equal(t, "1", string(serve(t, inst, get("/count")).Body), "and costs the realm nothing")
}

func TestWaitUntilIsBoundedByTheDeadline(t *testing.T) {
	for _, path := range []string{"/forever", "/spin"} {
		t.Run(path, func(t *testing.T) {
			f := newFixture(t, waiter)
			f.spec.Timeout = 200 * time.Millisecond
			p := f.pool()
			resp, err := p.Do(t.Context(), f.spec, get("/count"))
			require.NoError(t, err)
			require.Equal(t, "0", string(resp.Body))

			began := time.Now()
			resp, err = p.Do(t.Context(), f.spec, get(path))
			require.NoError(t, err)
			require.Equal(t, "answered", string(resp.Body))
			require.Less(t, time.Since(began), 150*time.Millisecond)
			require.Equal(t, 1, p.Warm(f.spec))
			require.Eventually(t, func() bool { return p.Warm(f.spec) == 0 }, 2*time.Second, 5*time.Millisecond,
				"the instance is retired at the deadline")

			inst := f.start()
			_, err = inst.Serve(context.Background(), get(path))
			require.ErrorIs(t, err, js.ErrDeadline)
		})
	}
}

func TestClosingEndsWhatIsRunning(t *testing.T) {
	f := newFixture(t, `
export default {
  async fetch(request, env, ctx) {
    if (request.url.endsWith("/linger")) {
      ctx.waitUntil(later(10000));
      return new Response("answered");
    }
    await later(10000);
    return new Response("late");
  },
};
`)
	inst := f.start()
	failed := make(chan error, 1)
	go func() {
		_, err := inst.Serve(context.Background(), get("/wait"))
		failed <- err
	}()
	time.Sleep(50 * time.Millisecond)

	began := time.Now()
	require.NoError(t, inst.Close())
	require.Less(t, time.Since(began), time.Second)
	require.Error(t, <-failed)
	require.NoError(t, inst.Close(), "twice is fine")
	_, err := inst.Serve(context.Background(), get("/"))
	require.ErrorIs(t, err, invoke.ErrNotServable)

	p, err := invoke.NewPool(invoke.WithRuntime(f.tier))
	require.NoError(t, err)
	_, err = p.Do(t.Context(), f.spec, get("/linger"))
	require.NoError(t, err)
	began = time.Now()
	require.NoError(t, p.Close())
	require.Less(t, time.Since(began), time.Second)
}

func TestConsoleGoesToTheInstancesLog(t *testing.T) {
	f := newFixture(t, `
export default {
  fetch() {
    console.log("plain", 1, true, null, undefined, 2n, { a: [1, "b"] }, () => {}, function named() {});
    console.info("info");
    console.debug("debug");
    console.trace("trace");
    console.warn("careful");
    console.error(new RangeError("bad range"));
    const cycle = {};
    cycle.self = cycle;
    console.log(cycle, Symbol("s"), { big: 3n });
    console.log({ get boom() { throw new Error("no"); } });
    console.log({ toJSON() { throw new Error("no"); }, toString() { throw new Error("no"); } });
    return new Response("logged");
  },
};
`)
	require.Equal(t, "logged", string(serve(t, f.start(), get("/")).Body))
	require.Equal(t, strings.Join([]string{
		`plain 1 true null undefined 2n {"a":[1,"b"]} [Function: (anonymous)] [Function: named]`,
		"info",
		"debug",
		"trace",
		`[object Object] Symbol(s) {"big":"3n"}`,
		"[object Object]",
		"[unprintable]",
		"",
	}, "\n"), f.logs.out())
	require.True(t, strings.HasPrefix(f.logs.err(), "careful\nRangeError: bad range\n"), f.logs.err())
}

func TestAFunctionWithNowhereToLogStillRuns(t *testing.T) {
	f := &fixture{t: t, store: &store{}, logs: &logs{}}
	f.store.put("asset", pack(t, map[string]string{"worker.js": `export default { fetch() { console.log("x"); console.error("y"); return new Response("ok"); } };`}))
	tier, err := js.New(f.store)
	require.NoError(t, err)
	f.tier = tier
	f.spec = invoke.Spec{Name: "quiet", Revision: "r1", Artifact: "asset"}
	require.Equal(t, "ok", string(serve(t, f.start(), get("/")).Body))
}

func TestALongLivedInstanceDoesNotFillItsArena(t *testing.T) {
	f := newFixture(t, `
const text = "x".repeat(128 << 10);
const bytes = new TextEncoder().encode(text);
export default {
  async fetch(request) {
    if (request.url.endsWith("/text")) return new Response(text);
    if (request.url.endsWith("/bytes")) return new Response(bytes);
    if (request.url.endsWith("/log")) { console.log(text); return new Response("logged"); }
    return new Response(String((await request.text()).length));
  },
};
`)
	f.spec.Memory = 16 << 20
	inst := f.start()
	upload := []byte(strings.Repeat("y", 128<<10))
	for range 150 {
		require.Len(t, serve(t, inst, get("/text")).Body, 128<<10, f.logs.err())
		require.Len(t, serve(t, inst, get("/bytes")).Body, 128<<10, f.logs.err())
		require.Equal(t, "logged", string(serve(t, inst, get("/log")).Body), f.logs.err())
		require.Equal(t, "131072", string(serve(t, inst, &invoke.Request{Method: "POST", Path: "/upload", Body: upload}).Body), f.logs.err())
	}
}
