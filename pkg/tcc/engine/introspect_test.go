package engine

import (
	"errors"
	"testing"

	"gotest.tools/v3/assert"
)

func introspectRoot() []*Node {
	return []*Node{
		DefineGroup("things", DefineIter([]*Attribute{
			String("id", Required(), Generated(GenCID)),
			String("label", Derived(DeriveDNSLabel, "id")),
			String("name", Required()),
			String("kind", Path("engine", Either("local", "remote")), Captures()),
			String("host", Path("engine", "remote", "host"), RequiredWhen("kind", "remote")),
			String("mode", Path("spec", "mode"), InSet("a", "b")),
			String("port", Path("spec", "port"), RequiredWhen("mode", "b")),
			String("either", Path(Either("x", "y"), "v"), Required()),
			String("anywhere", Path(All(), "v"), Required()),
			String("ghostly", RequiredWhen("nobody", "z")),
			String("dynamic", Path("spec", Either("p", "q")), Required()),
			String("needsDynamic", RequiredWhen("dynamic", "p")),
			String("nothing", Path(Either()), Required()),
			StringSlice("tags"),
		}, Resource("pkg", "Thing", "ThingSpec", "specs"))),
		DefineGroup("scopes", DefineIterGroup([]*Attribute{String("id")}).With(Leaf("scope"), Singular("Scope"))),
		DefineGroup("plain", DefineIterGroup([]*Attribute{String("id")})),
		DefineGroup("maps", DefineIter([]*Attribute{String("v")}, Singular("Map"))),
		DefineGroup("empty"),
		Define(All(), nil),
	}
}

func TestRequiredFieldsCarryTheirCondition(t *testing.T) {
	got := map[string]RequiredField{}
	for _, rf := range RequiredFields(introspectRoot(), "things") {
		got[joinPath(rf.Path)] = rf
	}

	assert.Assert(t, got["id"].When == nil && !got["id"].WhenBranch)
	assert.Assert(t, got["name"].When == nil)

	host := got["engine/remote/host"]
	assert.Assert(t, host.WhenBranch, "a Captures() discriminator has no value: its branch IS the key")
	assert.DeepEqual(t, host.WhenBranchAt, []string{"engine"})
	assert.DeepEqual(t, host.WhenIn, []string{"remote"})
	assert.Assert(t, host.When == nil)

	port := got["spec/port"]
	assert.DeepEqual(t, port.When.Path, []string{"spec", "mode"})
	assert.DeepEqual(t, port.WhenIn, []string{"b"})

	either := got["x/v"]
	assert.DeepEqual(t, either.AnyOf, [][]string{{"x", "v"}, {"y", "v"}})

	for _, absent := range []string{"anywhere", "ghostly", "needsDynamic", "nothing", "mode", "tags"} {
		_, ok := got[absent]
		assert.Assert(t, !ok, "%s has no addressable location or no addressable condition", absent)
	}
	_, ok := got["all/v"]
	assert.Assert(t, !ok)

	assert.Assert(t, RequiredFields(introspectRoot(), "ghost") == nil)
	assert.Assert(t, RequiredFields(introspectRoot(), "empty") == nil)
}

func joinPath(p []string) string {
	out := ""
	for i, s := range p {
		if i > 0 {
			out += "/"
		}
		out += s
	}
	return out
}

func TestCheckFieldsListsEveryPlainPath(t *testing.T) {
	var got []string
	for _, p := range CheckFields(introspectRoot(), "things") {
		got = append(got, joinPath(p))
	}
	assert.DeepEqual(t, got, []string{"id", "label", "name", "engine/remote/host", "spec/mode", "spec/port", "ghostly", "needsDynamic", "tags"})
	assert.Assert(t, CheckFields(introspectRoot(), "empty") == nil)
}

func TestBranchPrefixNeedsACapturedFiniteTail(t *testing.T) {
	for _, tc := range []struct {
		attr *Attribute
		ok   bool
		at   []string
	}{
		{String("k", Path("a", Either("x")), Captures()), true, []string{"a"}},
		{String("k", Path(Either("x")), Captures()), true, []string{}},
		{String("k", Path("a", Either("x"))), false, nil},
		{String("k", Captures()), false, nil},
		{String("k", Path("a", All()), Captures()), false, nil},
		{String("k", Path(Either("x"), "a", Either("y")), Captures()), false, nil},
	} {
		at, ok := branchPrefix(tc.attr)
		assert.Equal(t, ok, tc.ok, "%v", tc.attr.Path)
		if ok {
			assert.DeepEqual(t, at, tc.at)
		}
	}
}

func TestValidateFieldEnforcesTheDeclaredType(t *testing.T) {
	root := []*Node{DefineGroup("w", DefineIter([]*Attribute{
		Bool("on"),
		Int("n"),
		StringSlice("list"),
		String("s"),
	}))}
	for _, tc := range []struct {
		field []string
		value any
		ok    bool
	}{
		{[]string{"on"}, true, true},
		{[]string{"on"}, "true", false},
		{[]string{"n"}, 3, true},
		{[]string{"n"}, "3", true},
		{[]string{"n"}, "three", false},
		{[]string{"list"}, []string{"a"}, true},
		{[]string{"list"}, []any{"a", "b"}, true},
		{[]string{"list"}, []any{"a", 1}, false},
		{[]string{"list"}, "a", false},
		{[]string{"list", "0"}, "a", true},
		{[]string{"list", "0"}, true, false},
		{[]string{"s", "0"}, "a", false},
		{[]string{"list", ""}, "a", false},
		{[]string{"s"}, 12, true},
		{[]string{"s"}, false, false},
		{[]string{"s"}, nil, true},
	} {
		err := ValidateField(root, "w", tc.field, tc.value)
		assert.Equal(t, err == nil, tc.ok, "%v=%v: %v", tc.field, tc.value, err)
	}
}

func TestLayoutIsReadOffTheSchema(t *testing.T) {
	root := introspectRoot()
	assert.Equal(t, ContainerDoc(root, "scopes"), "scope")
	assert.Equal(t, ContainerDoc(root, "plain"), "", "nothing invents a document the schema did not name")
	assert.Equal(t, ContainerDoc(root, "things"), "", "a resource group is not a container")
	assert.Equal(t, ContainerDoc(root, "empty"), "")
	assert.Equal(t, ContainerDoc(root, "ghost"), "")

	assert.DeepEqual(t, Groups(root), []GroupInfo{
		{Group: "things", Singular: "ThingSpec"},
		{Group: "scopes", Singular: "Scope", Container: true},
		{Group: "plain", Container: true},
		{Group: "maps", Singular: "Map"},
		{Group: "empty"},
	})
}

func TestGeneratedAndDerivedFields(t *testing.T) {
	root := introspectRoot()
	assert.Equal(t, GeneratedBy(root, "things", []string{"id"}), GenCID)
	assert.Equal(t, GeneratedBy(root, "things", []string{"name"}), "")
	assert.Equal(t, GeneratedBy(root, "things", []string{"nope"}), "")

	a, b := mustGenerate(t, GenCID, "seed"), mustGenerate(t, GenCID, "seed")
	assert.Assert(t, a != b, "a generator mixes in its own entropy")
	assert.NilError(t, IsCIDValue(a))

	_, err := Generate("")
	assert.ErrorContains(t, err, "not a generated field")
	_, err = Generate("dice")
	assert.ErrorContains(t, err, `unknown generator "dice"`)

	x, err := Derive(DeriveResourceID, "src", "kind", "part")
	assert.NilError(t, err)
	y, err := Derive(DeriveResourceID, "src", "kind", "part")
	assert.NilError(t, err)
	assert.Equal(t, x, y)
	assert.NilError(t, IsCIDValue(x))
	z, err := Derive(DeriveResourceID, "src", "kindpart")
	assert.NilError(t, err)
	assert.Assert(t, z != x, "the inputs are joined so two splits of one string differ")

	_, err = Derive(DeriveResourceID)
	assert.ErrorContains(t, err, "derive from")
	_, err = Derive("")
	assert.ErrorContains(t, err, "not a derived field")
	_, err = Derive("spin")
	assert.ErrorContains(t, err, `unknown derivation "spin"`)
}

func mustGenerate(t *testing.T, kind string, seed ...any) string {
	t.Helper()
	v, err := Generate(kind, seed...)
	assert.NilError(t, err)
	return v
}

func IsCIDValue(v string) error {
	a := String("id", IsCID())
	return a.Validator(v)
}

func TestAnnotationsAreWhatGeneratorsRead(t *testing.T) {
	a := String("x",
		Doc("Title", "Description"),
		ShowWhen(Key("spec", "mode"), "a"),
		DefaultWhen("mode", "b", "c"),
		InSection("sec"),
		Field("GoX"),
		Accessor("X"),
		Ref("things", Inbound(), ByName(), Serves(), KindFrom(Key("route", "kind"))),
		EmitValidation("domain", "dns"),
		WireDrop(),
		Delegable(),
	)
	assert.Equal(t, a.Meta["label"], "Title")
	assert.Equal(t, a.Meta["doc"], "Description")
	assert.DeepEqual(t, a.Meta["showWhen"], ConditionSpec{Field: "spec/mode", In: []string{"a"}})
	assert.DeepEqual(t, a.Meta["defaultWhen"], ConditionSpec{Field: "mode", In: []string{"b", "c"}})
	assert.Equal(t, a.Meta["section"], "sec")
	assert.Equal(t, a.Meta["field"], "GoX")
	assert.Equal(t, a.Meta["accessor"], "X")
	assert.DeepEqual(t, a.Meta["ref"], RefSpec{Group: "things", Inbound: true, ByName: true, Serves: true, KindFrom: "route/kind"})
	assert.DeepEqual(t, a.Meta["emitValidation"], ValidationEmit{Key: "domain", Validator: "dns"})
	assert.Equal(t, a.Meta["wireDrop"], true)
	assert.Assert(t, IsDelegable(a))
	assert.Assert(t, !IsDelegable(String("y")))

	untitled := String("y", Doc("", ""))
	assert.Assert(t, untitled.Meta["label"] == nil && untitled.Meta["doc"] == nil)

	n := DefineIter(nil).With(
		GroupDoc("about"), Icon("bolt"),
		Section("one", "One", "first"), Section("two", "Two", "second"),
		AnswersAt("host"), Identity("id", "name"),
	)
	assert.Equal(t, n.Meta["doc"], "about")
	assert.Equal(t, n.Meta["icon"], "bolt")
	assert.DeepEqual(t, n.Meta["sections"], []SectionSpec{{ID: "one", Title: "One", Doc: "first"}, {ID: "two", Title: "Two", Doc: "second"}})
	assert.Equal(t, AnswersAtOf(n), "host")
	assert.Equal(t, AnswersAtOf(DefineIter(nil)), "")
	assert.Equal(t, AnswersAtOf(nil), "")
	id, name := IdentityOf(n)
	assert.Equal(t, id+"/"+name, "id/name")
	id, name = IdentityOf(nil)
	assert.Equal(t, id+name, "")
}

func TestLocatedErrorRendersWhatItKnows(t *testing.T) {
	inner := errors.New("boom")
	for _, tc := range []struct {
		err  *LocatedError
		want string
	}{
		{&LocatedError{Err: inner}, "boom"},
		{&LocatedError{File: "/a.yaml", Line: 3, Column: 4, Err: inner}, "/a.yaml:3:4: boom"},
		{&LocatedError{File: "/a.yaml", Line: 3, Err: inner}, "/a.yaml:3: boom"},
		{&LocatedError{File: "/a.yaml", Err: inner}, "/a.yaml: boom"},
	} {
		assert.Equal(t, tc.err.Error(), tc.want)
		assert.Assert(t, errors.Is(tc.err, inner))
	}
	assert.Equal(t, located("", 1, 1, inner), inner)
}

func TestNextValidationCarriesItsParts(t *testing.T) {
	ctx := map[string]any{"at": "x"}
	assert.DeepEqual(t, NewNextValidation("domain", "a.com", "dns", ctx),
		NextValidation{Key: "domain", Value: "a.com", Validator: "dns", Context: ctx})
}

func TestScalarValidatorsRejectWhatTheyCannotParse(t *testing.T) {
	d, b := Duration("d"), Bytes("b")
	assert.NilError(t, d.Validator("20s"))
	assert.ErrorContains(t, d.Validator("twenty"), "")
	assert.ErrorContains(t, d.Validator(20), "")
	assert.NilError(t, b.Validator("32MB"))
	assert.ErrorContains(t, b.Validator("lots"), "")
	assert.ErrorContains(t, b.Validator(32), "")
}
