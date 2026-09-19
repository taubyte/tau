package worker

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke/wasm"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// settings_test ties the Settings key space to the vocabulary.
//
// # The gap this closes
//
// A compiled resource is values at paths. This package files some of them into
// a `Settings` map keyed by strings, and every tier reads them back by the same
// strings — declared independently, in another package, with nothing comparing
// the two. So the ends agreed by copying, and three keys had drifted: `keys/ttl`
// and `keys/size` where the vocabulary says `ttl` and `size`, and `versioning`
// where it says `object/versioning`. Nothing failed. What an author wrote simply
// stopped being applied, and the const block above them claimed they were "the
// vocabulary's own paths".

// composed are keys this package BUILT rather than copied, so no attribute
// declares them. Every one needs a reason; a key arriving here without one is
// the drift this test exists to catch.
var composed = map[string]string{
	"entry/start/handle": "the start queue's HANDLE, which a tier opens by. The shell records `entry/start/name`, and a name is not openable without the project.",
	"invoke/name":        "what an `invoke` grant RESOLVED to. The attribute is the author's label, which may name a function of another name in another scope.",
	"invoke/namespace":   "where that target was found, as a resolved scope chain rather than the sentence that produced it.",
}

// carries names, per kind, the kinds whose settings may ride its grant.
//
// PERFORMING AN OPERATION ON A LOWERED KIND LANDS ON SOMETHING ELSE, and what
// it lands on has settings of its own that the performer needs. `start` on a
// workflow is a send to its first step's queue, so startedBy merges that
// queue's settings onto the workflow's grant — otherwise a tier would open the
// queue with library defaults while the dispatcher opened the same queue with
// what its author wrote.
var carries = map[string][]string{
	"workflow": {"sync"},
}

// entryField reads a key the entry record produced: `entry/<op>/<field>`.
//
// THE OPERATION IS AN ITER KEY, so it is chosen per operation and cannot be a
// static attribute name — `entry/start/kind` and `entry/invoke/kind` are one
// declaration. What IS declared is the field, so that is what gets checked.
func entryField(key string) (string, bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != "entry" {
		return "", false
	}
	return parts[2], true
}

// TestEVERYKEYTHISPACKAGEEMITSISONETHEVOCABULARYDECLARES.
func TestEveryEmittedSettingIsAnAttribute(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "function", "checkout")

	proj, _, err := h.Worker.At(t.Context(), "shop", "prod")
	require.NoError(t, err)

	bad := map[string][]string{}
	note := func(kind, key string) {
		if key == "" || schema.IsAttribute(kind, key) {
			return
		}
		if _, known := composed[key]; known {
			return
		}
		if field, ok := entryField(key); ok && schema.IsAttribute(kind, field) {
			return
		}
		for _, landing := range carries[kind] {
			if schema.IsAttribute(landing, key) {
				return
			}
		}
		bad[kind] = append(bad[kind], key)
	}

	var walk func(scope []string)
	walk = func(scope []string) {
		kinds, err := proj.Kinds(scope)
		if err != nil {
			return
		}
		for _, kind := range kinds {
			names, err := proj.Names(scope, kind)
			if err != nil {
				continue
			}
			for _, name := range names {
				spec, err := h.Worker.Resolve(t.Context(), Target{
					Project: "shop", Environment: "prod", Scope: scope, Kind: kind, Name: name,
				})
				if err != nil {
					// A kind no tier runs does not resolve; it has no settings
					// to check and its grants are checked from whoever reaches it.
					continue
				}
				for key := range spec.Settings {
					note(kind, key)
				}
				for _, g := range spec.Reaches {
					for key := range g.Settings {
						note(g.Kind, key)
					}
				}
			}
		}
		scopes, err := proj.Scopes(scope)
		if err != nil {
			return
		}
		for _, inner := range scopes {
			walk(append(append([]string{}, scope...), inner))
		}
	}
	walk(nil)

	if len(bad) == 0 {
		return
	}
	var lines []string
	for kind, keys := range bad {
		sort.Strings(keys)
		for _, key := range keys {
			lines = append(lines, kind+": "+key)
		}
	}
	sort.Strings(lines)
	require.Empty(t, lines,
		"these keys are not attributes of the kind they were filed against - either the schema renamed the attribute, or this package invented a path (see `composed` for how to declare one it built on purpose)")
}

// declared is every setting key this package spells, against the kind whose
// attribute it is meant to be.
//
// # Why this exists beside the test above
//
// That one walks what a compile actually EMITTED, so it catches a key this
// package invented. It cannot catch the opposite and more dangerous case: when
// the vocabulary renames an attribute, the read returns nothing, the key is
// simply absent from the map, and a walk over what was emitted sees nothing to
// object to. The tier then falls back to its own default and an author's
// declared visibility timeout, retry budget or quota stops taking effect with a
// green build.
//
// So the constants themselves are pinned. Renaming an attribute in the
// vocabulary now fails HERE, naming the constant that has to move with it.
var declared = map[string][]string{
	// instances/* and deliver/* sit on the kinds that HOLD instances and are
	// delivered to, which is where attachAccess puts them — not on every kind.
	"cache":    {settingTTL, settingSize, settingAdmission, settingLimit, settingValidator, settingValidated, settingDeliverPort, settingDeliverPath},
	"volume":   {settingQuota, settingCheckpointEvery, settingMountPath},
	"storage":  {settingStorageType, settingStorageSize, settingVersioning},
	"database": {settingType, "sql/" + settingCredentials, "sql/" + settingCredentialsScope},
	"sync":     {settingSyncType, "queue/max_attempts", "queue/visibility_timeout", "queue/delay", "queue/dead_letter/name", "log/consensus", "log/retention/age", "log/retention/size"},
	"channel":  {settingProtocol, settingServeKind, settingServeName, settingServeScope},
	"mcp":      {settingMCPCacheScope, settingMCPCacheTTL},
	"function": {settingDescription, settingAuthorizer, settingAuthorizerScope},
}

// TestEVERYKEYTHISPACKAGESPELLSSTILLNAMESANATTRIBUTE.
func TestEveryDeclaredSettingNamesAnAttribute(t *testing.T) {
	for kind, keys := range declared {
		for _, key := range keys {
			if field, ok := entryField(key); ok {
				require.True(t, schema.IsAttribute(kind, field),
					"%s has no field %q, so the entry record this package writes lands nowhere", kind, field)
				continue
			}
			require.True(t, schema.IsAttribute(kind, key),
				"%s declares no attribute %q — the vocabulary renamed it and this package still spells the old one, so the value silently stops travelling", kind, key)
		}
	}
}

// TestEVERYCLOSEDSETVALUETHISPACKAGESPELLSISSTILLINTHESET.
//
// A key that stops matching empties a map; a VALUE that stops matching does
// something worse, because these are compared for equality to decide whether to
// serve something. Rename `object` in the vocabulary and every storage in the
// cloud is refused as "not the type we serve"; rename `authorizer` and every
// guard is refused as not being one. Both are loud, and both blame the wrong
// thing — which is why the literal is pinned rather than trusted.
func TestEveryClosedSetValueIsStillInItsSet(t *testing.T) {
	require.Contains(t, schema.StorageTypes(), objectStorage,
		"the one storage model this cloud serves is no longer a model the vocabulary has")
	require.Contains(t, schema.SyncTypes(), queueType)
	require.Contains(t, schema.SyncTypes(), logType,
		"a sync type this package switches on is no longer one the vocabulary declares")
	require.Contains(t, schema.HostWorlds(), wasm.WorldAuthorizer,
		"the world a guard must be built against is no longer a world")

	// AND THE SWITCH COVERS THE SET. syncSettings carries a type's own fields,
	// and a type it has no arm for travels with none of them — silently, since
	// the default arm returns what it has. This is the one place that notices.
	covered := map[string]bool{queueType: true, logType: true}
	for _, model := range schema.SyncTypes() {
		require.True(t, covered[model],
			"the vocabulary declares a %q sync and syncSettings has no arm for it, so none of its fields reach a tier", model)
	}
}
