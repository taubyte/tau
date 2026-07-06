package gen

import (
	"fmt"
	"strings"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

// The mapping rules turn one DSL *engine.Attribute into the exported Go accessor
// (name + type + body) that pkg/schema hand-writes today. See the plan file for
// the derivation of every rule below.

// commonAttrs come from TaubyteAttributes and are emitted by the fixed universal
// template block (Id/Description/Tags), not from the DSL walk. "name" is the
// resource identity (Name() reads the struct field), never a config accessor.
var commonAttrs = map[string]bool{"id": true, "name": true, "description": true, "tags": true}

// nameOverrides pin the exported accessor name where the derived name (last plain
// Path segment) does not match the existing public API.
var nameOverrides = map[string]string{
	"domains.fqdn":        "FQDN",
	"storages.ttl":        "TTL",
	"messaging.match":     "ChannelMatch",
	"messaging.mqtt":      "MQTT",
	"messaging.websocket": "WebSocket",
}

// skipSet: emit the getter but NOT the setter — the write is folded into a
// combined hand-written helper (Replicas/Channel/Bridges/Object/Streaming).
var skipSet = keySet(
	"databases.replicas-min", "databases.replicas-max",
	"messaging.match", "messaging.regex", "messaging.mqtt", "messaging.websocket",
	"storages.versioning", "storages.ttl",
)

// skipGet: emit the setter but NOT the getter — the read applies a value
// transform the DSL can't express (domains.fqdn lower-cases).
var skipGet = keySet(
	"domains.fqdn",
)

// skipBoth: neither accessor is mechanical — value transforms (network-access
// bool<->string), combined encryption, or deep github-* folded into Git()/Github().
// Keys use the DSL group name (note: the group is "websites", not "website").
var skipBoth = keySet(
	"functions.http-methods", // TO IMPLEMENT in the DSL; no accessor exists yet
	"databases.network-access", "databases.encryption-type", "databases.encryption-key",
	"storages.network-access",
	"libraries.github-id", "libraries.github-fullname",
	"websites.github-id", "websites.github-fullname",
)

// goType maps a DSL type to the Go type used by the schema accessors. Float is
// unused by resource schemas; "" signals "skip".
func goType(t engine.Type) string {
	switch t {
	case engine.TypeString:
		return "string"
	case engine.TypeStringSlice:
		return "[]string"
	case engine.TypeBool:
		return "bool"
	case engine.TypeInt:
		return "int"
	default:
		return ""
	}
}

// plainSegs returns the path as plain strings; ok is false if any segment is a
// matcher (Either/All) — those locations are dynamic and not mechanically emittable.
func plainSegs(path []engine.StringMatch) (segs []string, ok bool) {
	for _, p := range path {
		s, isStr := p.(string)
		if !isStr {
			return nil, false
		}
		segs = append(segs, s)
	}
	return segs, true
}

// pathSegs is the CANONICAL config location: Path, or the bare attribute name.
// The tcc engine resolves Path first (engine/node.go setAttributes), so setters
// write here and getters read here.
func pathSegs(a *engine.Attribute) (segs []string, ok bool) {
	if len(a.Path) > 0 {
		return plainSegs(a.Path)
	}
	return []string{a.Name}, true
}

// compatSegs is the legacy ALIAS location, if the attribute declares one. The
// engine falls back to it when the canonical Path is absent, so generated
// getters do the same (canonical read, compat read-fallback).
func compatSegs(a *engine.Attribute) (segs []string, ok bool) {
	if len(a.Compat) == 0 {
		return nil, false
	}
	return plainSegs(a.Compat)
}

// accessorName is the exported Go name: an override, else the last plain Path
// segment title-cased, else the attribute name title-cased.
func accessorName(group string, a *engine.Attribute) string {
	if ov, has := nameOverrides[group+"."+a.Name]; has {
		return ov
	}
	base := a.Name
	if segs, ok := plainSegs(a.Path); ok && len(segs) > 0 {
		base = segs[len(segs)-1]
	}
	return title(base)
}

func setBody(segs []string) string {
	q := quoteAll(segs)
	if len(segs) == 1 {
		return fmt.Sprintf("return basic.Set(%s, value)", q[0])
	}
	return fmt.Sprintf("return basic.SetChild(%s, %s, value)", q[0], q[1])
}

func getBody(goT string, segs []string) string {
	return fmt.Sprintf("return basic.Get[%s](g, %s)", goT, strings.Join(quoteAll(segs), ", "))
}

// getBodyCompat reads the canonical path, falling back to the compat alias when
// the canonical key is absent (mirrors the tcc engine's Path-then-Compat read in
// engine/node.go). Used only when the compat has no distinct deprecated accessor.
func getBodyCompat(goT string, path, compat []string) string {
	return fmt.Sprintf("var v %s\nif %s.Value(&v) == nil {\nreturn v\n}\n%s",
		goT, chain(path), getBody(goT, compat))
}

// chain builds a g.Config().Get(...).Get(...) query for a config path.
func chain(segs []string) string {
	var b strings.Builder
	b.WriteString("g.Config()")
	for _, seg := range segs {
		fmt.Fprintf(&b, ".Get(%q)", seg)
	}
	return b.String()
}

func title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func quoteAll(segs []string) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

func keySet(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}
