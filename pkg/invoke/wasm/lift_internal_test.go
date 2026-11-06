package wasm

import (
	"fmt"
	"testing"

	"github.com/samyfodil/wazy/component"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
)

// A stranger's component may answer anything; each malformed shape is an error
// naming the export, never a panic.

func TestLiftingAResponseThatIsNotOne(t *testing.T) {
	for name, value := range map[string]component.Value{
		"not a record":    "hello",
		"too few fields":  []component.Value{uint32(200)},
		"too many fields": []component.Value{uint32(200), nil, []byte{}, "extra"},
		"a status that is not one": []component.Value{
			"200", []component.Value{}, []byte{},
		},
		"a body that is not bytes": []component.Value{
			uint32(200), []component.Value{}, "a string",
		},
		"headers that are not a list": []component.Value{
			uint32(200), "not a list", []byte{},
		},
		"a header that is not a pair": []component.Value{
			uint32(200), []component.Value{[]component.Value{"only"}}, []byte{},
		},
		"a header that is not two strings": []component.Value{
			uint32(200), []component.Value{[]component.Value{"name", uint32(1)}}, []byte{},
		},
		"a header that is not a record": []component.Value{
			uint32(200), []component.Value{"flat"}, []byte{},
		},
	} {
		_, err := responseFrom(value, "checkout")
		require.ErrorIs(t, err, ErrNotAHandler, name)
		require.ErrorContains(t, err, "checkout", name)
	}
}

func TestLiftingAnEmptyResponse(t *testing.T) {
	res, err := responseFrom([]component.Value{uint32(204), nil, nil}, "checkout")
	require.NoError(t, err)
	require.Equal(t, 204, res.Status)
	require.Nil(t, res.Headers)
	require.Nil(t, res.Body)

	res, err = responseFrom([]component.Value{uint32(200), []component.Value{}, []byte{}}, "checkout")
	require.NoError(t, err)
	require.Nil(t, res.Headers, "an empty list is no headers, not an empty map somebody has to check")
	require.Empty(t, res.Body)
}

func TestLoweringARequest(t *testing.T) {
	args := requestArgs(&invoke.Request{
		Method:  "POST",
		Path:    "/orders",
		Headers: map[string][]string{"b": {"2"}, "a": {"1", "1b"}},
		Body:    []byte("payload"),
	})
	require.Len(t, args, 1)
	fields, ok := args[0].([]component.Value)
	require.True(t, ok)
	require.Len(t, fields, 4)

	require.Equal(t, "POST", fields[0])
	require.Equal(t, "/orders", fields[1])
	require.Equal(t, []byte("payload"), fields[3])

	// Sorted with repeats in order, so two instances get the same list for one request.
	require.Equal(t, []component.Value{
		[]component.Value{"a", "1"},
		[]component.Value{"a", "1b"},
		[]component.Value{"b", "2"},
	}, fields[2])

	// A nil body is an empty list: list<u8> has no absent case.
	fields, _ = requestArgs(&invoke.Request{})[0].([]component.Value)
	require.Equal(t, []byte{}, fields[3])
	require.Empty(t, fields[2])
}

func TestTheMemoryCeilingInPages(t *testing.T) {
	require.Equal(t, uint32(0), memoryPages(0), "no ceiling declared is no ceiling set")
	require.Equal(t, uint32(0), memoryPages(-1))

	require.Equal(t, uint32(1), memoryPages(1))
	require.Equal(t, uint32(1), memoryPages(pageSize))
	require.Equal(t, uint32(2), memoryPages(pageSize+1))
	require.Equal(t, uint32(489), memoryPages(32_000_000), "32MB, the fixture's own function limit")

	// THE 32-BIT ADDRESS SPACE IS THE REAL CEILING. A larger number panics
	// inside wazy rather than being honoured, so it is clamped here.
	require.Equal(t, uint32(1<<16), memoryPages(1<<40))
}

func TestTheEnvironmentIsSorted(t *testing.T) {
	require.Nil(t, environment(nil))
	require.Nil(t, environment(map[string]string{}))
	require.Equal(t,
		[]string{"A=1", "B=2", "C=3"},
		environment(map[string]string{"C": "3", "A": "1", "B": "2"}))
}

func TestLiftingRepeatedHeadersKeepsEachNamesValuesApart(t *testing.T) {
	res, err := responseFrom([]component.Value{uint32(200), []component.Value{
		[]component.Value{"set-cookie", "a=1"},
		[]component.Value{"vary", "accept"},
		[]component.Value{"set-cookie", "b=2"},
	}, []byte{}}, "checkout")
	require.NoError(t, err)
	require.Equal(t, map[string][]string{"set-cookie": {"a=1", "b=2"}, "vary": {"accept"}}, res.Headers)

	res.Headers["vary"] = append(res.Headers["vary"], "origin")
	require.Equal(t, []string{"a=1", "b=2"}, res.Headers["set-cookie"], "an append to one name never reaches another's")
}

func TestLoweringManyHeadersAndAnUnknownMethod(t *testing.T) {
	headers := map[string][]string{}
	for i := range 40 {
		headers[fmt.Sprintf("x-%02d", i)] = []string{fmt.Sprint(i)}
	}
	fields := requestArgs(&invoke.Request{Method: "BREW", Path: "/pot", Headers: headers})[0].([]component.Value)
	require.Equal(t, "BREW", fields[0])
	list := fields[2].([]component.Value)
	require.Len(t, list, 40)
	for i, h := range list {
		require.Equal(t, []component.Value{fmt.Sprintf("x-%02d", i), fmt.Sprint(i)}, h, "past the names held on the stack, still sorted")
	}
}

func TestLoweringARequestAllocatesOnceAndPerHeader(t *testing.T) {
	req := &invoke.Request{Method: "GET", Path: "/orders", Headers: map[string][]string{"x-trace": {"abc"}, "accept": {"*/*"}}, Body: []byte("x")}
	// The array, the record, the path, the header list and the body are boxed once
	// each; a header costs its name, its value and its pair.
	allocs := testing.AllocsPerRun(100, func() { requestArgs(req) })
	require.LessOrEqual(t, allocs, float64(5+3*2))
}
