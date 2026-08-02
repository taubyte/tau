package engine

import (
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/object"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

// load one attribute out of a one-line YAML document.
func loadAttr(t *testing.T, body string, attr *Attribute) (any, error) {
	t.Helper()
	fs := afero.NewMemMapFs()
	assert.NilError(t, fs.MkdirAll("/t", 0755))
	assert.NilError(t, afero.WriteFile(fs, "/t/"+attr.Name+".yaml", []byte(body), 0644))
	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/t"))
	assert.NilError(t, err)
	obj, err := load[object.Refrence](&Node{Attributes: []*Attribute{attr}}, sr.Query())
	if err != nil {
		return nil, err
	}
	return obj.Get(attr.Name), nil
}

// YAML infers a scalar's type from how it was written; the DSL declares what the
// field means. Where the two disagree only about notation, the value is
// converted rather than dropped — a repository id authored unquoted used to fail
// its string read and vanish from the compiled object entirely.
func TestScalarNotationIsTolerated(t *testing.T) {
	t.Run("an unquoted number satisfies a string field", func(t *testing.T) {
		v, err := loadAttr(t, "485476045\n", String("s"))
		assert.NilError(t, err)
		assert.Equal(t, v, "485476045")
	})

	t.Run("a quoted number satisfies an integer field", func(t *testing.T) {
		v, err := loadAttr(t, `"8080"`+"\n", Int("i"))
		assert.NilError(t, err)
		assert.Equal(t, v, 8080)
	})

	t.Run("a non-numeric string in an integer field is still an error", func(t *testing.T) {
		_, err := loadAttr(t, "not_a_number\n", Int("i"))
		assert.Assert(t, err != nil, "a value that is not a number must not be coerced into one")
	})

	t.Run("a number is not a boolean", func(t *testing.T) {
		// Nothing is coerced INTO a bool: 1 is not true. This is the case the
		// notation argument does not cover.
		_, err := loadAttr(t, "1\n", Bool("b"))
		assert.ErrorContains(t, err, "wrong type")
	})

	t.Run("a mistyped value is reported, not dropped", func(t *testing.T) {
		// The field is optional, so the old behaviour was to swallow the read
		// failure and carry on — which is how a mistyped domains list deployed
		// an unroutable function. Present-and-wrong is now an error.
		_, err := loadAttr(t, "notanumber\n", Int("i"))
		assert.ErrorContains(t, err, "wrong type")
	})

	// NB a bool authored where a string is declared decodes to "true": go-yaml
	// renders any scalar into a string, before any of this. Partial validation
	// is stricter (it rejects a Go bool for a string field), which is the safe
	// direction — an editor warns about something the compiler would accept,
	// never the reverse.
	t.Run("yaml itself renders a bool into a string field", func(t *testing.T) {
		v, err := loadAttr(t, "true\n", String("s"))
		assert.NilError(t, err)
		assert.Equal(t, v, "true")
	})
}

// Partial validation must accept exactly what will compile, or an editor reports
// an error on a document the compiler is about to take.
func TestCheckTypeAgreesWithCoercion(t *testing.T) {
	root := []*Node{DefineGroup("g", DefineIter([]*Attribute{
		String("s"), Int("i"), Bool("b"),
	}))}
	assert.NilError(t, ValidateField(root, "g", []string{"s"}, 485476045))
	assert.NilError(t, ValidateField(root, "g", []string{"i"}, "8080"))
	assert.ErrorContains(t, ValidateField(root, "g", []string{"i"}, "eighty"), "expects integer")
	assert.ErrorContains(t, ValidateField(root, "g", []string{"s"}, true), "expects string")
	assert.ErrorContains(t, ValidateField(root, "g", []string{"b"}, 1), "expects boolean")

	// Numeric-ness is decided by KIND on both sides. Two hand-written type lists
	// had already drifted: int32 was an acceptable integer but not an acceptable
	// string, so the same value passed one field and failed the other.
	for _, n := range []any{int8(5), int16(5), int32(5), int64(5), uint(5), uint32(5), float32(1.5), 5.5} {
		assert.NilError(t, ValidateField(root, "g", []string{"s"}, n),
			"every numeric kind renders into a string field: %T", n)
	}
	for _, n := range []any{int8(5), int16(5), int32(5), uint32(5)} {
		assert.NilError(t, ValidateField(root, "g", []string{"i"}, n),
			"every whole-number kind is an integer: %T", n)
	}
	// ...but a float is not a whole number, however it was written.
	assert.ErrorContains(t, ValidateField(root, "g", []string{"i"}, 5.5), "expects integer")
}
