package worker

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

func TestARemoteModelCarriesWhereAndHowItIsCalled(t *testing.T) {
	p := fixture(t)
	opus, _, err := p.Resource(nil, specs.KindModel, "opus")
	require.NoError(t, err)

	got := modelSettings(opus)
	require.Equal(t, "claude-opus-5", got[specs.ModelKeyIdentifier])
	require.Equal(t, specs.ModelFormatAnthropic, got[specs.ModelKeyFormat])
	require.Equal(t, specs.ModelInferenceKindRemote, got[specs.ModelInferenceKeyKind])
	require.Equal(t, "https://api.anthropic.com", got[specs.ModelInferenceKeyRemoteEndpoint])
	require.NotEmpty(t, got[specs.ModelInferenceKeyRemoteCredentialsName], "a reference compiles to an id")
	require.Equal(t, "0.2", got[specs.ModelParametersKeyTemperature])
	require.Equal(t, "4096", got[specs.ModelParametersKeyMaxTokens])
	require.NotContains(t, got, specs.ModelInferenceKeyLocalName, "a remote model names no workload")
}

func TestALocalModelIsResolvedToItsWorkloadsScope(t *testing.T) {
	p := fixture(t)
	embed, id, err := p.Resource(nil, specs.KindModel, "embed")
	require.NoError(t, err)

	got := modelSettings(embed)
	require.Equal(t, specs.ModelInferenceKindLocal, got[specs.ModelInferenceKeyKind])
	require.Equal(t, "pg", got[specs.ModelInferenceKeyLocalPort])
	require.NotEmpty(t, got[specs.ModelInferenceKeyLocalName])

	require.Equal(t, map[string]string{settingLocalScope: ""}, localModelScope(p, embed, id))

	opus, opusID, err := p.Resource(nil, specs.KindModel, "opus")
	require.NoError(t, err)
	require.Nil(t, localModelScope(p, opus, opusID), "a remote model has no workload to find")
	require.Nil(t, localModelScope(p, embed, "QmNotInThisProject"), "nor does one this project cannot locate")
}

func TestAModelsStopSequencesArriveWhole(t *testing.T) {
	got := modelSettings(specs.Resource{"parameters": map[string]any{
		"top_p": 0.9, "stop": []any{"END", "STOP"},
	}})
	require.Equal(t, map[string]string{
		specs.ModelParametersKeyTopP: "0.9",
		specs.ModelParametersKeyStop: "END\nSTOP",
	}, got)

	require.Nil(t, modelSettings(specs.Resource{}))
}

func TestAnMcpCarriesHowWidelyItsListingMayBeCached(t *testing.T) {
	p := fixture(t)
	tools, _, err := p.Resource(nil, specs.KindMcp, "tools")
	require.NoError(t, err)

	require.Equal(t, map[string]string{
		settingMCPCacheScope: specs.McpToolsCacheScopePrincipal,
		settingMCPCacheTTL:   "30s",
	}, mcpSettings(specs.ReadMcp(tools).Tools))
	require.Nil(t, mcpSettings(specs.McpTools{}))
}
