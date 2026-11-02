package wasm

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/samyfodil/wazy/component"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
)

func TestTheTransportAndWarnOptionsAreKept(t *testing.T) {
	var said string
	r := &Runtime{}
	WithTransport(refuse{})(r)
	WithWarn(func(format string, args ...any) { said = fmt.Sprintf(format, args...) })(r)

	require.Equal(t, refuse{}, r.transport)
	r.warn("%s unserved", "vector")
	require.Equal(t, "vector unserved", said)
}

func TestEveryGoErrorLandsOnItsArm(t *testing.T) {
	wrap := func(err error) error { return fmt.Errorf("wrapped: %w", err) }
	other := errors.New("down")

	type testCase struct {
		name string
		got  *component.VariantValue
		disc uint32
	}
	for _, c := range []testCase{
		{"log not writable", logArm(wrap(ErrNotWritable)), errLogNotWritable},
		{"log too large", logArm(wrap(ErrRecordTooLarge)), errLogTooLarge},
		{"log bad cursor", logArm(wrap(ErrBadCursor)), errBadCursor},
		{"log otherwise", logArm(other), errLogUnavailable},
		{"vector wrong width", vectorArm(wrap(ErrWrongWidth)), errWrongWidth},
		{"vector malformed", vectorArm(wrap(ErrMalformed)), errVectorMalformed},
		{"vector otherwise", vectorArm(other), errVectorUnavailable},
		{"kv otherwise", kvArm(other), errKVUnavailable},
	} {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.disc, c.got.Disc)
			require.NotEmpty(t, c.got.Payload)
		})
	}

	miss := kvArm(wrap(ErrNotFound))
	require.Equal(t, errKVNotFound, miss.Disc)
	require.Nil(t, miss.Payload, "a miss carries no detail")
}

func TestFloatsLiftFromEitherListShape(t *testing.T) {
	got, err := readFloats([]float32{1, 2})
	require.NoError(t, err)
	require.Equal(t, []float32{1, 2}, got)

	got, err = readFloats([]component.Value{float32(3), float32(4)})
	require.NoError(t, err)
	require.Equal(t, []float32{3, 4}, got)

	_, err = readFloats([]component.Value{float32(3), "four"})
	require.ErrorContains(t, err, "element 1")

	_, err = readFloats("not a list")
	require.ErrorContains(t, err, "expected a list of f32")
}

func TestToolCallsThatAreNotToolCalls(t *testing.T) {
	calls, err := toolCallsFrom(nil)
	require.NoError(t, err)
	require.Nil(t, calls)

	calls, err = toolCallsFrom([]component.Value{[]component.Value{"id", "name", "{}"}})
	require.NoError(t, err)
	require.Equal(t, []invoke.ToolCall{{ID: "id", Name: "name", Arguments: "{}"}}, calls)

	for name, value := range map[string]component.Value{
		"not a list":        "calls",
		"not a record":      []component.Value{"flat"},
		"too few fields":    []component.Value{[]component.Value{"id", "name"}},
		"not three strings": []component.Value{[]component.Value{"id", "name", uint32(1)}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := toolCallsFrom(value)
			require.Error(t, err)
		})
	}
}

func TestAnInferenceThatIsNotOne(t *testing.T) {
	good := func() []component.Value {
		return []component.Value{nil, nil, nil, nil, nil}
	}
	with := func(i int, v component.Value) []component.Value {
		f := good()
		f[i] = v
		return f
	}
	for name, value := range map[string]component.Value{
		"not a record":             "request",
		"messages not a list":      with(0, "m"),
		"a message not a record":   with(0, []component.Value{"m"}),
		"a message's strings":      with(0, []component.Value{[]component.Value{"user", uint32(1), "", nil}}),
		"a message's tool calls":   with(0, []component.Value{[]component.Value{"user", "hi", "", "calls"}}),
		"stop not a list":          with(3, "stop"),
		"tools not a list":         with(4, "tools"),
		"a tool not a record":      with(4, []component.Value{"t"}),
		"a tool not three strings": with(4, []component.Value{[]component.Value{"t", "d", uint32(1)}}),
		"a tool with no name":      with(4, []component.Value{[]component.Value{"", "d", "{}"}}),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := inferenceFrom(value)
			require.Error(t, err)
		})
	}

	got, err := inferenceFrom([]component.Value{
		nil, float64(0), uint32(9), []component.Value{"END", uint32(1)},
		[]component.Value{[]component.Value{"t", "d", "{}"}},
	})
	require.NoError(t, err)
	require.NotNil(t, got.Temperature)
	require.Equal(t, uint32(9), *got.MaxTokens)
	require.Equal(t, []string{"END"}, got.Stop, "a stop that is not a string is dropped")
	require.Equal(t, []invoke.Tool{{Name: "t", Description: "d", Schema: "{}"}}, got.Tools)
}

func TestAnAnswerOfNothingLowersEmpty(t *testing.T) {
	require.Equal(t, []component.Value{"", "", []component.Value{}, []component.Value{uint32(0), uint32(0)}}, answerValue(nil))
}

func TestAShapingThatIsNotOne(t *testing.T) {
	for name, value := range map[string]component.Value{
		"no verdict":        "pass",
		"pass not a chunk":  component.VariantValue{Disc: verdictPass, Payload: "x"},
		"pass not bytes":    component.VariantValue{Disc: verdictPass, Payload: []component.Value{"out", []byte{}}},
		"an undeclared arm": component.VariantValue{Disc: 9},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := shapingFrom(value, "shape")
			require.ErrorIs(t, err, ErrNotAProxyTransformer)
		})
	}

	got, err := shapingFrom(&component.VariantValue{Disc: verdictPass, Payload: []component.Value{[]byte("o"), nil}}, "shape")
	require.NoError(t, err)
	require.Equal(t, []byte("o"), got.Pass.Out)
	require.Nil(t, got.Pass.State)

	got, err = shapingFrom(component.VariantValue{Disc: verdictClose}, "shape")
	require.NoError(t, err)
	require.Equal(t, "the transformer closed it", got.Refused)
}

func TestADirectionThatIsNotOne(t *testing.T) {
	for name, value := range map[string]component.Value{
		"no decision":          "to",
		"to not a destination": component.VariantValue{Disc: directTo, Payload: "x"},
		"to not four strings":  component.VariantValue{Disc: directTo, Payload: []component.Value{"k", "n", "m", uint32(1)}},
		"to nothing":           component.VariantValue{Disc: directTo, Payload: []component.Value{"", "n", "m", "80"}},
		"more of no count":     component.VariantValue{Disc: directMore, Payload: "x"},
		"more of zero":         component.VariantValue{Disc: directMore, Payload: uint32(0)},
		"an undeclared arm":    component.VariantValue{Disc: 9},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := directionFrom(value, "direct")
			require.ErrorIs(t, err, ErrNotAProxyRouter)
		})
	}

	got, err := directionFrom(&component.VariantValue{Disc: directMore, Payload: uint32(4)}, "direct")
	require.NoError(t, err)
	require.Equal(t, uint32(4), got.More)

	got, err = directionFrom(component.VariantValue{Disc: directRefuse}, "direct")
	require.NoError(t, err)
	require.Equal(t, "the router refused it", got.Refused)

	require.Equal(t, []component.Value{"", "", "", "", ""}, connectionValue(nil))
}

func TestARoutingThatIsNotOne(t *testing.T) {
	for name, value := range map[string]component.Value{
		"no decision":          "to",
		"to not a destination": component.VariantValue{Disc: routeTo, Payload: "x"},
		"to not three strings": component.VariantValue{Disc: routeTo, Payload: []component.Value{"k", "n", uint32(1)}},
		"to nothing":           component.VariantValue{Disc: routeTo, Payload: []component.Value{"k", "n", ""}},
		"an undeclared arm":    component.VariantValue{Disc: 9},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := routingFrom(value, "route")
			require.ErrorIs(t, err, ErrNotARouter)
		})
	}

	_, err := routingFrom(component.VariantValue{Disc: routeAnswer, Payload: "x"}, "route")
	require.ErrorIs(t, err, ErrNotAHandler, "an answer is lifted as any response is")

	got, err := routingFrom(&component.VariantValue{Disc: routeTo, Payload: []component.Value{"function", "", "f"}}, "route")
	require.NoError(t, err)
	require.Equal(t, &invoke.Destination{Kind: "function", Name: "f"}, got.To)
}

func TestAnOutcomeThatIsNotOne(t *testing.T) {
	for name, value := range map[string]component.Value{
		"no outcome":            "forward",
		"forward not a request": component.VariantValue{Disc: outcomeForward, Payload: "x"},
		"forward a bad method":  component.VariantValue{Disc: outcomeForward, Payload: []component.Value{uint32(1), "/", nil, nil}},
		"forward bad headers":   component.VariantValue{Disc: outcomeForward, Payload: []component.Value{"GET", "/", "h", nil}},
		"forward a bad body":    component.VariantValue{Disc: outcomeForward, Payload: []component.Value{"GET", "/", nil, "body"}},
		"an answer that is not": component.VariantValue{Disc: outcomeAnswer, Payload: "x"},
		"an undeclared arm":     component.VariantValue{Disc: 9},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := outcomeFrom(value, "transform")
			require.Error(t, err)
		})
	}

	got, err := outcomeFrom(&component.VariantValue{Disc: outcomeForward, Payload: []component.Value{
		"GET", "/", []component.Value{[]component.Value{"x-shaped", "1"}}, nil,
	}}, "transform")
	require.NoError(t, err)
	require.Equal(t, []string{"1"}, got.Forward.Headers["X-Shaped"])
}

func TestAnAdmissionThatIsNotOne(t *testing.T) {
	for name, value := range map[string]component.Value{
		"no decision":           "granted",
		"denied with no reason": component.VariantValue{Disc: admissionDenied, Payload: uint32(1)},
		"granted not a list":    component.VariantValue{Disc: admissionGranted, Payload: "read"},
		"granted not strings":   component.VariantValue{Disc: admissionGranted, Payload: []component.Value{uint32(1)}},
		"an undeclared arm":     component.VariantValue{Disc: 9},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := admissionFrom(value, "authorize")
			require.ErrorIs(t, err, ErrNotAnHTTPAuthorizer)
		})
	}

	got, err := admissionFrom(&component.VariantValue{Disc: admissionDenied, Payload: "no"}, "authorize")
	require.NoError(t, err)
	require.Equal(t, "no", got.Denied)
}

func TestAVerdictThatIsNotOne(t *testing.T) {
	for name, value := range map[string]component.Value{
		"no verdict":            "admitted",
		"refused in a number":   component.VariantValue{Disc: verdictRefused, Payload: uint32(1)},
		"undecided in a number": component.VariantValue{Disc: verdictUndecided, Payload: uint32(1)},
		"an undeclared arm":     component.VariantValue{Disc: 9},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := verdictFrom(value, "admit")
			require.ErrorIs(t, err, ErrNotAValidator)
		})
	}

	got, err := verdictFrom(&component.VariantValue{Disc: verdictRefused}, "admit")
	require.NoError(t, err)
	require.Equal(t, Verdict{}, got, "a nil reason is no reason given")
}

func TestSQLValuesCrossBothWays(t *testing.T) {
	type testCase struct {
		in   any
		want component.Value
	}
	for _, c := range []testCase{
		{nil, component.VariantValue{Disc: valNull}},
		{"s", component.VariantValue{Disc: valText, Payload: "s"}},
		{[]byte("b"), component.VariantValue{Disc: valBlob, Payload: []byte("b")}},
		{true, component.VariantValue{Disc: valBoolean, Payload: true}},
		{int64(1), component.VariantValue{Disc: valInteger, Payload: int64(1)}},
		{2, component.VariantValue{Disc: valInteger, Payload: int64(2)}},
		{int32(3), component.VariantValue{Disc: valInteger, Payload: int64(3)}},
		{1.5, component.VariantValue{Disc: valReal, Payload: 1.5}},
		{float32(2.5), component.VariantValue{Disc: valReal, Payload: 2.5}},
		{time.Duration(0), component.VariantValue{Disc: valText, Payload: "0s"}},
	} {
		require.Equal(t, c.want, lowerSQL(c.in), "%T", c.in)
	}

	_, _, _, err := statementArgs("query", []component.Value{"db", "select 1"})
	require.ErrorContains(t, err, "expected 3 arguments")

	_, _, _, err = statementArgs("query", []component.Value{"db", uint32(1), nil})
	require.Error(t, err)

	_, _, _, err = statementArgs("query", []component.Value{"db", "select 1", "args"})
	require.ErrorContains(t, err, "want a list")

	_, _, _, err = statementArgs("query", []component.Value{"db", "select ?", []component.Value{"bare"}})
	require.ErrorContains(t, err, "argument 0")

	label, statement, bound, err := statementArgs("query", []component.Value{"db", "select 1", nil})
	require.NoError(t, err)
	require.Equal(t, "db", label)
	require.Equal(t, "select 1", statement)
	require.Nil(t, bound)

	_, err = liftSQL(component.VariantValue{Disc: 99})
	require.Error(t, err)
}

func TestBytesOfRefusesAString(t *testing.T) {
	_, ok := bytesOf("x")
	require.False(t, ok)
}
