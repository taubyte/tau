package gen

import (
	"testing"

	"github.com/stretchr/testify/require"

	v2 "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// model_test pins the decisions model.go makes about TYPES, against the real
// vocabulary: which fields are entries, which blocks are one type, and what a
// reader can and cannot say about what the author wrote.

func modelOf(t *testing.T) (*model, map[string]*typeDef) {
	t.Helper()
	var kinds []string
	for _, g := range v2.GenerationRoot() {
		if name, _ := g.Match.(string); name != "" && len(g.Children) > 0 {
			kinds = append(kinds, name)
		}
	}
	m := buildModel(v2.GenerationRoot(), kinds, reservedNames("specs"))
	byName := map[string]*typeDef{}
	for _, td := range m.order {
		byName[td.name] = td
	}
	return m, byName
}

func memberOf(t *testing.T, td *typeDef, name string) member {
	t.Helper()
	for _, mem := range td.members {
		if mem.name == name {
			return mem
		}
	}
	t.Fatalf("%s has no field %s", td.name, name)
	return member{}
}

// TestANESTEDGROUPISENTRIESNOTFIELDS — the bug this replaced: a port's number
// and a step's attempts read off the kind.
func TestANestedGroupIsEntriesNotFields(t *testing.T) {
	for _, l := range Locations(v2.GenerationRoot(), "application") {
		require.NotEqual(t, "number", l.Path(), "a port's number is not the application's")
	}

	_, types := modelOf(t)
	ports := memberOf(t, types["Application"], "Ports")
	require.Equal(t, "map[string]ApplicationPortsEntry", ports.goType())

	steps := memberOf(t, types["Workflow"], "Steps")
	require.Equal(t, "[]WorkflowStepsEntry", steps.goType(), "a DefineList is ordered")
	require.True(t, steps.sub.list)
}

// TestABLOCKEVERYKINDAGREESONISONETYPE, and one they disagree on is not.
func TestSharedBlocksAreFoundNotListed(t *testing.T) {
	_, types := modelOf(t)

	for _, kind := range []string{"Application", "Function", "Storage", "Workflow"} {
		require.Equal(t, "Regions", memberOf(t, types[kind], "Regions").goType(), kind)
	}
	require.Equal(t, "Instances", memberOf(t, types["Database"], "Instances").goType())
	require.Equal(t, "Authorizer", memberOf(t, types["Cache"], "Authorizer").goType())
	require.Equal(t, "[]TransformersEntry", memberOf(t, types["Proxy"], "Transformers").goType(),
		"the gateway's and the proxy's transformers are one declaration")
	require.Equal(t, "map[string]VariablesEntry", memberOf(t, types["Website"], "Variables").goType(),
		"a different legal set for `at` is still one entry")

	require.Equal(t, "FunctionExecution", memberOf(t, types["Function"], "Execution").goType(),
		"a function's execution is not an application's")
	require.Equal(t, "ApplicationExecution", memberOf(t, types["Application"], "Execution").goType())

	// THE UNION OF LEGAL VALUES, for a shared entry validated differently.
	at := memberOf(t, types["VariablesEntry"], "At")
	require.ElementsMatch(t, []string{"run", "build", "both"}, at.loc.Enum)
}

// TestANAMETHECORETAKESISNOTSHADOWED. `Host` is the core's.
func TestACoreNameIsNotShadowed(t *testing.T) {
	_, types := modelOf(t)
	require.Equal(t, "HostBlock", memberOf(t, types["Gateway"], "Host").goType())
}

// TestABLOCKOFDEFAULTSCANNOTSAYITWASWRITTEN, and the generator says so rather
// than emitting a Declared that is always true.
func TestABlockOfDefaultsHasNoDeclared(t *testing.T) {
	_, types := modelOf(t)
	require.False(t, types["VolumeCheckpoint"].hasDeclared)
	require.Contains(t, string(types["VolumeCheckpoint"].render()), "EVERY FIELD IN IT IS DEFAULTED")

	live := types["ApplicationLiveness"]
	require.True(t, live.hasDeclared)
	for _, k := range live.declared {
		require.NotEqual(t, []string{"liveness", "failures"}, k, "a defaulted field is not evidence")
	}
}

// TestTHESHORTFORMISTHEBLOCKSVALUE. `source: services/api` sits at the key a
// block of the same name reads under.
func TestTheShortFormIsTheBlocksValue(t *testing.T) {
	_, types := modelOf(t)
	v := memberOf(t, types["FunctionSource"], "Value")
	require.Equal(t, []string{"source"}, v.key)
}

// TestCONSTANTSARENAMEDFROMTHESCHEMA.
func TestConstantsAreNamedFromTheSchema(t *testing.T) {
	m, types := modelOf(t)

	got := map[string]string{}
	for _, td := range m.order {
		for _, c := range td.constants() {
			got[c.name] = c.value
		}
	}
	require.Equal(t, `"queue/max_attempts"`, got["SyncQueueKeyMaxAttempts"])
	require.Equal(t, `"sql"`, got["DatabaseTypeSql"])
	require.Equal(t, `"verify-full"`, got["DatabaseSqlTlsVerifyFull"])
	require.Equal(t, `"closed"`, got["TransformersEntryFailClosed"])
	require.Equal(t, `"POST"`, got["GatewayRouteMethodsPOST"], "a list of strings carries string values")
	require.NotNil(t, types["Model"])

	require.Contains(t, string(m.renderKindNames()), `KindApplication = "application"`)
}

func TestValueIdent(t *testing.T) {
	require.Equal(t, "VerifyFull", valueIdent("verify-full"))
	require.Equal(t, "MountRw", valueIdent("mount-rw"))
	require.Equal(t, "Empty", valueIdent("--"))
}

func TestATakenNameIsRefused(t *testing.T) {
	taken := map[string]bool{"Regions": true}
	require.Panics(t, func() { claim(taken, "Regions") })
	require.NotPanics(t, func() { claim(taken, "Instances") })
}
