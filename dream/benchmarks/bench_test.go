//go:build dreaming

// Profiling benchmarks over a live dream universe. Run with:
//
//	make bench-dreaming BENCH=HTTPFunction FLAGS="-cpuprofile=/tmp/cpu.prof -memprofile=/tmp/mem.prof"
//
// then `go tool pprof -top /tmp/cpu.prof` / `go tool pprof -top -alloc_space /tmp/mem.prof`.
package benchmarks

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sync"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	"github.com/taubyte/tau/pkg/specs/common"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	"github.com/taubyte/tau/pkg/specs/methods"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/monkey/fixtures/compile"
	tcc "github.com/taubyte/tau/utils/tcc"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	_ "github.com/taubyte/tau/dream/fixtures"
	_ "github.com/taubyte/tau/pkg/tcc/taubyte/v1/fixtures"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/substrate/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
)

const testFqdn = "hal.computers.com"

var (
	testProjectId   = "QmegMKBQmDTU9FUGKdhPFn1ZEtwcNaCA2wmyLW8vJn7wZN"
	testFunctionId  = "QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J51Tv"
	testFunction2Id = "QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J5456"
)

var benchServices = map[string]commonIface.ServiceConfig{
	"tns":       {},
	"substrate": {},
	"hoarder":   {},
}

var (
	sharedOnce sync.Once
	sharedU    *dream.Universe
	sharedErr  error
)

// sharedUniverse boots one universe serving the prebuilt ping.zwasm function
// and reuses it across benchmarks — boot is measured separately by
// BenchmarkUniverseBoot.
func sharedUniverse(b *testing.B) *dream.Universe {
	b.Helper()
	sharedOnce.Do(func() { sharedErr = bootShared() })
	if sharedErr != nil {
		b.Fatal(sharedErr)
	}
	return sharedU
}

func bootShared() error {
	m, err := dream.New(context.Background())
	if err != nil {
		return err
	}

	u, err := m.New(dream.UniverseConfig{Name: "bench"})
	if err != nil {
		return err
	}

	err = u.StartWithConfig(&dream.Config{
		Services: benchServices,
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Hoarder: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		return err
	}

	fs, _, err := tcc.GenerateProject(testProjectId,
		// well-provisioned: plenty of headroom over the wasm linear memory
		// (≥128KiB), so instances pool and /ping measures the warm path.
		&structureSpec.Function{
			Id:      testFunctionId,
			Name:    "someFunc",
			Type:    "http",
			Call:    "ping",
			Memory:  20 * 1024 * 1024,
			Source:  ".",
			Timeout: 1000000000,
			Method:  "GET",
			Domains: []string{"someDomain"},
			Paths:   []string{"/ping"},
		},
		// under-provisioned: the wasm linear memory already fills the 2-page
		// cap, so every instance is retired on Free and /ping2 measures the
		// full cold-start path (fetch, unzip, decompress, compile, attach).
		&structureSpec.Function{
			Id:      testFunction2Id,
			Name:    "someColdFunc",
			Type:    "http",
			Call:    "ping",
			Memory:  100000,
			Source:  ".",
			Timeout: 1000000000,
			Method:  "GET",
			Domains: []string{"someDomain"},
			Paths:   []string{"/ping2"},
		},
		&structureSpec.Domain{
			Name: "someDomain",
			Fqdn: testFqdn,
		},
	)
	if err != nil {
		return err
	}

	if err = u.RunFixture("injectProject", fs); err != nil {
		return err
	}

	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	zwasm := path.Join(wd, "..", "..", "services", "monkey", "fixtures", "compile", "assets", "ping.zwasm")
	for _, id := range []string{testFunctionId, testFunction2Id} {
		err = u.RunFixture("compileFor", compile.BasicCompileFor{
			ProjectId:  testProjectId,
			ResourceId: id,
			Paths:      []string{zwasm},
		})
		if err != nil {
			return err
		}
	}

	// warm until both functions resolve and serve
	client := commonTest.CreateHttpClient()
	for _, route := range []string{"/ping", "/ping2"} {
		for i := 0; ; i++ {
			body, err := callRoute(u, client, route)
			if err == nil && string(body) == "PONG" {
				break
			}
			if i >= 60 {
				return fmt.Errorf("function on %s never came up: %v (body: %q)", route, err, body)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}

	sharedU = u
	return nil
}

func routeURL(u *dream.Universe, route string) (string, error) {
	port, err := u.GetPortHttp(u.Substrate().Node())
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://%s:%d%s", testFqdn, port, route), nil
}

func callRoute(u *dream.Universe, client *http.Client, route string) ([]byte, error) {
	url, err := routeURL(u, route)
	if err != nil {
		return nil, err
	}
	res, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return io.ReadAll(res.Body)
}

// BenchmarkHTTPFunction measures the warm serving path: HTTP router → domain/
// TNS lookup → cached wasm instance call → response.
func BenchmarkHTTPFunction(b *testing.B) {
	benchRoute(b, "/ping")
}

// BenchmarkHTTPFunctionColdStart measures the full per-request cold-start
// path (fetch, unzip, decompress, compile, attach): someColdFunc's memory
// config leaves no growth headroom, so every instance is retired on Free.
func BenchmarkHTTPFunctionColdStart(b *testing.B) {
	benchRoute(b, "/ping2")
}

func benchRoute(b *testing.B, route string) {
	b.Helper()
	u := sharedUniverse(b)
	client := commonTest.CreateHttpClient()
	url, err := routeURL(u, route)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for b.Loop() {
		res, err := client.Get(url)
		if err != nil {
			b.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			b.Fatal(err)
		}
		if string(body) != "PONG" {
			b.Fatalf("expected PONG got %q", body)
		}
	}
}

// BenchmarkHTTPFunctionParallel exposes lock contention in the serving path.
func BenchmarkHTTPFunctionParallel(b *testing.B) {
	u := sharedUniverse(b)
	url, err := routeURL(u, "/ping")
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		client := commonTest.CreateHttpClient()
		for pb.Next() {
			res, err := client.Get(url)
			if err != nil {
				b.Fatal(err)
			}
			body, err := io.ReadAll(res.Body)
			res.Body.Close()
			if err != nil {
				b.Fatal(err)
			}
			if string(body) != "PONG" {
				b.Fatalf("expected PONG got %q", body)
			}
		}
	})
}

// BenchmarkTNSFetch measures the p2p TNS resolution path substrate hits on
// every cache miss.
func BenchmarkTNSFetch(b *testing.B) {
	u := sharedUniverse(b)
	tnsClient := u.Substrate().Tns()

	httpPath, err := methods.HttpPath(testFqdn, functionSpec.PathVariable)
	if err != nil {
		b.Fatal(err)
	}
	linksPath := httpPath.Versioning().Links()

	b.ReportAllocs()
	for b.Loop() {
		obj, err := tnsClient.Fetch(linksPath)
		if err != nil {
			b.Fatal(err)
		}
		if _, err = obj.Current(common.DefaultBranches); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkUniverseBoot measures cold start of a tns+substrate+hoarder
// universe (keypair gen, libp2p swarm, service init). Teardown is untimed.
// Run with -benchtime=5x — each iteration takes seconds.
func BenchmarkUniverseBoot(b *testing.B) {
	m, err := dream.New(context.Background())
	if err != nil {
		b.Fatal(err)
	}
	defer m.Close()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		u, err := m.New(dream.UniverseConfig{Name: fmt.Sprintf("boot-%d", i)})
		if err != nil {
			b.Fatal(err)
		}
		if err = u.StartWithConfig(&dream.Config{Services: benchServices}); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		u.Stop()
		b.StartTimer()
	}
}
