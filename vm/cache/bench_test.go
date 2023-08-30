package cache

import (
	"fmt"
	"testing"
	"time"
)

/*
To run this test:

dream new multiverse

dream inject importProdProject \
--pid QmcjcsAio5T45a2vGfrm5XpGDLfF9f2gzAWEhVkGo9sa1j \
-t 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

Then run this test
*/

var (
	testIterations = 1000
	domain         = "http://hal.computers.com:9630"
	path           = "ping"
)

func init() {
	domain = "https://fto71a120.g.noose.ink"
}

func TestParallelismBasic(t *testing.T) {
	t.Skip("need to run as a dreamland test")
	err := ParallelGetWithBodyCheck(
		testIterations,
		GetTester{Url: domain, FailingResponse: &ResponseCheck{Body: []byte("pong")}},
		GetTester{Url: domain + "/" + path, PassingResponse: &ResponseCheck{Body: []byte("pong")}},
	)
	if err != nil {
		t.Error(err)
		return
	}
}

func TestParallelismWeb(t *testing.T) {
	t.Skip("need to run as a dreamland test")
	now := time.Now()
	err := ParallelGetWithBodyCheck(testIterations, GetTester{Url: domain, FailingResponse: &ResponseCheck{Body: []byte("pong")}})
	if err != nil {
		t.Error(err)
		return
	}

	fmt.Printf("Over %d iterations average %s per concurrent website call \n", testIterations, time.Since(now)/time.Duration(testIterations))
}

func TestParallelismFunc(t *testing.T) {
	now := time.Now()
	err := ParallelGetWithBodyCheck(testIterations, GetTester{Url: domain + "/" + path, PassingResponse: &ResponseCheck{Body: []byte("pong0")}})
	if err != nil {
		t.Error(err)
		return
	}

	fmt.Printf("Over %d iterations average %s per concurrent function call \n", testIterations, time.Since(now)/time.Duration(testIterations))

}
