package test

import (
	"net/http"
	"testing"

	_ "bitbucket.org/taubyte/billing/service"
	_ "bitbucket.org/taubyte/config-compiler/fixtures"
	bench "bitbucket.org/taubyte/go-node-tvm/cache"
	structureSpec "github.com/taubyte/go-specs/structure"
	_ "github.com/taubyte/odo/protocols/hoarder/service"
	_ "github.com/taubyte/odo/protocols/node/service"
	_ "github.com/taubyte/odo/protocols/tns/service"
)

var (
	testProjectId = "QmegMKBQmDTU9FUGKdhPFn1ZEtwcNaCA2wmyLW8vJn7wZN"

	testFuncId1   = "QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J51Tv"
	testFunc1Path = "/ping1"

	testFunc2Path = "/ping2"
	testFuncId2   = "QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J51Ty"

	testFqdn = "hal.computers.com"

	httpCalls = 300

	testStructures []interface{} = []interface{}{
		&structureSpec.Function{
			Id:      testFuncId1,
			Name:    "counterFunc",
			Type:    "http",
			Memory:  10000000,
			Timeout: 5000000000,
			Method:  "GET",
			Source:  ".",
			Call:    "countertest",
			Paths:   []string{testFunc1Path},
			Domains: []string{"someDomain"},
		},
		&structureSpec.Function{
			Id:      testFuncId2,
			Name:    "counterFunc2",
			Type:    "http",
			Memory:  10000000,
			Timeout: 5000000000,
			Method:  "GET",
			Source:  ".",
			Call:    "countertest2",
			Paths:   []string{testFunc2Path},
			Domains: []string{"someDomain"},
		},
		&structureSpec.Domain{
			Name: "someDomain",
			Fqdn: testFqdn,
		},
	}
)

func TestConcurrent(t *testing.T) {
	u, err := startUniverse(testStructures)
	defer u.Stop()
	if err != nil {
		t.Errorf("Starting universe failed with: %s", err)
		return
	}

	defs, err := getDefs(testStructures)
	if err != nil {
		t.Errorf("Getting structure definitions failed with: %s", err)
		return
	}

	urls, err := getUrls(u, defs)
	if err != nil {
		t.Errorf("Getting testing urls failed with: %s", err)
		return
	}

	err = bench.ParallelGet(httpCalls, urls...)
	if err != nil {
		t.Errorf("Parallel get failed with: %s", err)
		return
	}

	counterMetrics, err := checkKeys(u, testProjectId, testStructures)
	if err != nil {
		t.Errorf("Checking counter metrics failed with: %s", err)
		return
	}

	for _, metric := range counterMetrics {
		if metric.successCount != httpCalls {
			t.Errorf("Counted only `%d` successful calls, expected `%d`", metric.successCount, httpCalls)
			return
		}
		metric.display()
	}
}

func TestBasic(t *testing.T) {
	u, err := startUniverse(testStructures)
	if err != nil {
		t.Errorf("Starting universe failed with: %s", err)
		return
	}
	defer u.Stop()

	defs, err := getDefs(testStructures)
	if err != nil {
		t.Errorf("Getting structure definitions failed with: %s", err)
		return
	}

	urls, err := getUrls(u, defs)
	if err != nil {
		t.Errorf("Getting testing urls failed with: %s", err)
		return
	}

	for i := 0; i < httpCalls; i++ {
		for _, url := range urls {
			_, err := http.DefaultClient.Get(url)
			if err != nil {
				t.Errorf("Http request for `%s` failed with: %s", url, err)
				return
			}
		}

	}

	counterMetrics, err := checkKeys(u, testProjectId, testStructures)
	if err != nil {
		t.Errorf("Checking counter metrics failed with: %s", err)
		return
	}

	for _, metric := range counterMetrics {
		if metric.successCount != httpCalls {
			t.Errorf("Counted only `%d` successful calls, expected `%d`", metric.successCount, httpCalls)
			return
		}
		metric.display()
	}
}
