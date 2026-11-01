package schema_test

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/taubyte/tau/pkg/tcc/interp"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gopkg.in/yaml.v3"
	"gotest.tools/v3/assert"
)

func TestWorkflowCarriesBackWhatItCarriesAway(t *testing.T) {
	got := schema.Carriages("workflow")
	by := map[string]interp.Carriage{}
	for _, c := range got {
		by[c.Field+" "+c.In+" -> "+c.Into+"/"+c.As] = c
	}
	assert.Equal(t, len(got), 10, "got %v", got)

	// a step's attempts becomes its queue's max-attempts, whole
	attempts := by["attempts steps -> sync/queue/max_attempts"]
	assert.DeepEqual(t, attempts, interp.Carriage{
		Field: "attempts", In: "steps", Into: "sync", As: "queue/max_attempts",
	}, cmpopts.IgnoreUnexported(interp.Term{}, interp.Carriage{}))
	assert.Assert(t, !attempts.Composed())

	// A step's name is composed into its queue's name and still comes back: the
	// Concat term knows which underscore it wrote.
	name := by["name steps -> sync/@"]
	assert.Equal(t, name.Field, "name")
	assert.Equal(t, name.In, "steps")
	assert.Assert(t, name.Composed())
	assert.Equal(t, name.Before.String(), "{name}_", "what stands before it, as declared")
	assert.Equal(t, name.After.String(), "")

	// The step's name lands in the DRIVER's name too, and the driver is what its queue
	// writes into — one step, two produced resources, the same name in both.
	assert.Assert(t, by["name steps -> function/@"].Composed(), "the driver is named for the step: %v", got)
	assert.Assert(t, by["name steps -> sync/>"].Composed(), "the queue names its consumer: %v", got)

	// A WAIT COMES BACK AS THE STRING IT WAS WRITTEN AS. It is carried onto the queue's
	// `delay`, which is a Duration — so tcc parses "30s" going in and formats it going
	// out, and the step never held a number.
	assert.Assert(t, !by["wait steps -> sync/queue/delay"].Composed())

	// and where failure goes comes back from the queue's dead-letter, which is where the
	// lowering put it
	onError := by["on_error steps -> sync/queue/dead_letter/name"]
	assert.Equal(t, onError.Field, "on_error")
	assert.Assert(t, onError.Composed())

	remainder := map[string]bool{}
	for _, f := range schema.Remainder("workflow") {
		remainder[f] = true
	}
	for _, kept := range []string{"timeout", "concurrency", "description"} {
		assert.Assert(t, remainder[kept], "%s comes back from nothing, so a shell has to keep it", kept)
	}
	for _, carried := range []string{"steps/attempts", "steps/name", "steps/on_error"} {
		assert.Assert(t, !remainder[carried], "%s is carried, so a shell storing it stores it twice", carried)
	}
	// The function a step calls is in the write's path, not carried, so the shell
	// keeps it, along with `input` and `result`, which only the driver reads.
	for _, kept := range []string{"steps/call/function", "steps/call/input", "steps/call/result"} {
		assert.Assert(t, remainder[kept], "%s comes back from nothing: %v", kept, schema.Remainder("workflow"))
	}
}

func TestWorkflowValuesReadBackFromWhatItBecame(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	got := schema.ReadBack(schema.LoweringPlan(obj.Flat()), schema.Carriages("workflow"))

	var authored struct {
		Steps []struct {
			Name     string `yaml:"name"`
			Wait     string `yaml:"wait"`
			Attempts int    `yaml:"attempts"`
			Backoff  struct {
				Interval string  `yaml:"interval"`
				Factor   float64 `yaml:"factor"`
				Ceiling  string  `yaml:"ceiling"`
			} `yaml:"backoff"`
		} `yaml:"steps"`
	}
	body, err := os.ReadFile("../fixtures/basic/workflow/checkout.yaml")
	assert.NilError(t, err)
	assert.NilError(t, yaml.Unmarshal(body, &authored))
	assert.Assert(t, len(authored.Steps) > 0)

	want := map[string]string{}
	for i, s := range authored.Steps {
		at := "steps/" + strconv.Itoa(i)
		// a step that writes no attempts still carries the default, and the default is
		// what the queue was built with, so it is what comes back
		attempts := s.Attempts
		if attempts == 0 {
			attempts = 1
		}
		want[at+"/attempts"] = strconv.Itoa(attempts)
		// defaulted, and it comes back from the queue's dead-letter
		want[at+"/on_error"] = "failed"
		// the composed one: the queue is called `checkout_reserve`, and `reserve` is what
		// is left once the prefix the lowering declared is taken off it
		want[at+"/name"] = s.Name
		if s.Wait != "" {
			want[at+"/wait"] = s.Wait
		}
		// and the schedule, which comes back off the queue that enforces it
		if s.Backoff.Interval != "" {
			want[at+"/backoff/interval"] = s.Backoff.Interval
			want[at+"/backoff/ceiling"] = s.Backoff.Ceiling
			want[at+"/backoff/factor"] = strconv.FormatFloat(s.Backoff.Factor, 'f', -1, 64)
		}
	}
	assert.DeepEqual(t, got, want)
}

func TestAgentValuesReadBackInAuthoredForm(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	got := schema.ReadBack(schema.LoweringPlan(obj.Flat()), schema.Carriages("agent"))
	// the loop's ceilings are carried onto what it became, and come back in the form
	// they were written in — tcc's own scalar inverse
	assert.Equal(t, got["limits/timeout"], "5m0s")
	assert.Equal(t, got["limits/memory"], "128MB")
	delete(got, "limits/timeout")
	delete(got, "limits/memory")

	var authored struct {
		Memory struct {
			Dimensions int    `yaml:"dimensions"`
			Size       string `yaml:"size"`
		} `yaml:"memory"`
	}
	body, err := os.ReadFile("../fixtures/basic/agent/support.yaml")
	assert.NilError(t, err)
	assert.NilError(t, yaml.Unmarshal(body, &authored))

	assert.DeepEqual(t, got, map[string]string{
		"memory/dimensions": strconv.Itoa(authored.Memory.Dimensions),
		"memory/size":       authored.Memory.Size,
	})
}
