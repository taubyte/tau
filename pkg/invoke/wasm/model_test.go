package wasm_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// model_test drives the REAL component through GENERATED bindings, which is the
// only thing that proves the record shapes in model.wit and the type table in
// model.go agree. Everything else about this capability could be right and a
// field in the wrong position would still hand the guest somebody else's value.

// thinking is an inference backing that records what it was asked.
type thinking struct {
	mu   sync.Mutex
	saw  *invoke.Inference
	seen invoke.Grant
	err  error

	answer invoke.Answer
}

func newThinking() *thinking {
	return &thinking{answer: invoke.Answer{
		Content:    "an answer",
		StopReason: "stop",
		Usage:      invoke.Usage{InputTokens: 7, OutputTokens: 11},
	}}
}

func (i *thinking) Infer(_ context.Context, grant invoke.Grant, req *invoke.Inference) (*invoke.Answer, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.saw, i.seen = req, grant
	if i.err != nil {
		return nil, i.err
	}
	out := i.answer
	return &out, nil
}

func (i *thinking) asked() *invoke.Inference {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.saw
}

func modelGrant(label string, ops []string, settings map[string]string) []invoke.Grant {
	return []invoke.Grant{{
		Kind: "model", Label: label, Handle: "shop/prod/QmModel",
		Ops: ops, Settings: settings,
	}}
}

// TestEVERYFIELDOFTHEANSWERCROSSES, not only the text — which is what a
// fixture echoing the whole record is for.
func TestAComponentCallsAModelAndReadsTheWholeAnswer(t *testing.T) {
	brain := newThinking()

	instance := start(t, tier(t, cacher(t), wasm.WithInference(brain)), invoke.Spec{
		Name: "fn", Reaches: modelGrant("brain", []string{"infer"}, nil),
	})

	res := call(t, instance, "/infer/brain/", []byte("what is it"))
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "an answer|stop|7|11", string(res.Body))

	// And the request crossed the other way intact.
	asked := brain.asked()
	require.NotNil(t, asked)
	require.Len(t, asked.Messages, 1)
	require.Equal(t, "user", asked.Messages[0].Role)
	require.Equal(t, "what is it", asked.Messages[0].Content)
}

// TestAGUESTCANNOTRAISETHECEILINGITSAUTHORWROTE. The fixture asks for a
// million tokens on every call; what the backing sees is the resource's number.
func TestAGuestsRequestIsClampedToTheResourcesCeiling(t *testing.T) {
	brain := newThinking()

	instance := start(t, tier(t, cacher(t), wasm.WithInference(brain)), invoke.Spec{
		Name: "fn",
		Reaches: modelGrant("brain", []string{"infer"},
			map[string]string{wasm.SettingMaxTokens: "512"}),
	})

	require.Equal(t, 200, call(t, instance, "/infer/brain/", []byte("hi")).Status)

	asked := brain.asked()
	require.NotNil(t, asked.MaxTokens)
	require.Equal(t, uint32(512), *asked.MaxTokens, "the author's ceiling, not the guest's million")
}

// TestNODECLAREDCEILINGISTHEGUESTSOWN, which is the vocabulary's default:
// absent means the provider's.
func TestWithNoDeclaredCeilingTheGuestsRequestStands(t *testing.T) {
	brain := newThinking()

	instance := start(t, tier(t, cacher(t), wasm.WithInference(brain)), invoke.Spec{
		Name: "fn", Reaches: modelGrant("brain", []string{"infer"}, nil),
	})

	require.Equal(t, 200, call(t, instance, "/infer/brain/", []byte("hi")).Status)

	asked := brain.asked()
	require.NotNil(t, asked.MaxTokens)
	require.Equal(t, uint32(1_000_000), *asked.MaxTokens)
}

// TestAMODELITDIDNOTDECLARE is its own arm, which reads as a typo rather than
// as an outage.
func TestCallingAModelWithNoGrantIsNoSuchModel(t *testing.T) {
	instance := start(t, tier(t, cacher(t), wasm.WithInference(newThinking())), invoke.Spec{
		Name: "fn", Reaches: modelGrant("brain", []string{"infer"}, nil),
	})

	res := call(t, instance, "/infer/other/", []byte("hi"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "no-such-model:other", string(res.Body))
}

// TestAGRANTWITHOUTTHEVERB is denied. `infer` is the one verb this kind has,
// so a grant that does not carry it is a resource that may name the model and
// not call it.
func TestAModelGrantedWithoutInferIsDenied(t *testing.T) {
	instance := start(t, tier(t, cacher(t), wasm.WithInference(newThinking())), invoke.Spec{
		Name: "fn", Reaches: modelGrant("brain", nil, nil),
	})

	res := call(t, instance, "/infer/brain/", []byte("hi"))
	require.Equal(t, 403, res.Status)
	require.True(t, strings.HasPrefix(string(res.Body), "denied:"), string(res.Body))
}

// TestAREFUSEDTARGETISREFUSEDPERMODEL, not per workload: a cloud that will not
// serve one model must not take the whole resource down.
func TestARefusedModelIsDeniedWithItsReason(t *testing.T) {
	grants := modelGrant("brain", []string{"infer"}, nil)
	grants[0].Refused = "no provider for that region"

	instance := start(t, tier(t, cacher(t), wasm.WithInference(newThinking())), invoke.Spec{
		Name: "fn", Reaches: grants,
	})

	res := call(t, instance, "/infer/brain/", []byte("hi"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "denied:no provider for that region", string(res.Body))
}

// TestTHETWOFAILUREARMSARETOLDAPART. A caller retries `unavailable` and wastes
// its budget retrying `refused`.
func TestARefusalAndAnOutageAreDifferentArms(t *testing.T) {
	for _, c := range []struct {
		err  error
		says string
	}{
		{wasm.ErrRefusedByProvider, "refused:"},
		{errors.New("dial tcp: no route to host"), "unavailable:"},
	} {
		brain := newThinking()
		brain.err = c.err

		instance := start(t, tier(t, cacher(t), wasm.WithInference(brain)), invoke.Spec{
			Name: "fn", Reaches: modelGrant("brain", []string{"infer"}, nil),
		})

		res := call(t, instance, "/infer/brain/", []byte("hi"))
		require.Equal(t, 403, res.Status)
		require.True(t, strings.HasPrefix(string(res.Body), c.says), string(res.Body))
	}
}

// TestAGRANTWITHNOTHINGBEHINDIT is refused at Start rather than at the first
// call, which is the rule every capability here follows: a workload that would
// fail every request touching its model should not look healthy until somebody
// exercises that path.
func TestAModelGrantWithNoBackingIsRefusedAtStart(t *testing.T) {
	_, err := tier(t, cacher(t)).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafycacher",
		Reaches: modelGrant("brain", []string{"infer"}, nil),
	})
	require.ErrorIs(t, err, wasm.ErrNoInference)
}

// TestTHEMODELASKSANDTHECALLERRUNSIT. Nothing in the host runs a tool, which
// is what keeps a loop the caller's program rather than a behaviour hidden
// inside a host function — so what this proves is that the ASKING crosses, in
// both directions, through generated bindings.
func TestAComponentSendsToolsAndReadsBackWhatTheModelWants(t *testing.T) {
	brain := newThinking()
	brain.answer = invoke.Answer{
		Content:    "let me look",
		StopReason: "tool-calls",
		ToolCalls: []invoke.ToolCall{
			{ID: "call_1", Name: "lookup", Arguments: `{"id":"42"}`},
			{ID: "call_2", Name: "lookup", Arguments: `{"id":"43"}`},
		},
	}

	instance := start(t, tier(t, cacher(t), wasm.WithInference(brain)), invoke.Spec{
		Name: "fn", Reaches: modelGrant("brain", []string{"infer"}, nil),
	})

	res := call(t, instance, "/infer-tools/brain/", []byte("find 42"))
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "let me look|tool-calls|call_1:lookup:{\"id\":\"42\"},call_2:lookup:{\"id\":\"43\"}",
		string(res.Body), "both calls, with their ids — which is how a caller tells two answers apart")

	// And the tool the guest declared reached the backing.
	asked := brain.asked()
	require.Len(t, asked.Tools, 1)
	require.Equal(t, "lookup", asked.Tools[0].Name)
	require.Equal(t, "finds one", asked.Tools[0].Description)
	require.JSONEq(t, `{"type":"object","properties":{"id":{"type":"string"}}}`, asked.Tools[0].Schema)
}

// TestAPLAINCOMPLETIONSENDSNOTOOLS, so a caller that wants prose pays nothing
// for a feature it is not using.
func TestACompletionWithNoToolsCarriesNone(t *testing.T) {
	brain := newThinking()

	instance := start(t, tier(t, cacher(t), wasm.WithInference(brain)), invoke.Spec{
		Name: "fn", Reaches: modelGrant("brain", []string{"infer"}, nil),
	})

	require.Equal(t, 200, call(t, instance, "/infer/brain/", []byte("hi")).Status)
	require.Empty(t, brain.asked().Tools)
}
