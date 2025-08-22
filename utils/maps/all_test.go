package maps

import "testing"

var fixture map[string]interface{} = map[string]interface{}{
	"string": "hello world",
	"hex":    "0xFF",
	"int":    123,
	"bool":   true,
}

func TestString(t *testing.T) {
	s, err := String(fixture, "string")
	if err != nil {
		t.Errorf("Error extracting String from map: %s", err.Error())
	} else if s != fixture["string"] {
		t.Errorf("Error extracting String from map: `%s` != `%s`", s, fixture["string"])
	}
}

func TestHexInt(t *testing.T) {
	h, err := HexInt(fixture, "hex")
	if err != nil {
		t.Errorf("Error extracting Hex from map: %s", err.Error())
	} else if h != 255 {
		t.Errorf("Error extracting Hex from map: %d != 255", h)
	}
}

func TestInt(t *testing.T) {
	i, err := Int(fixture, "int")
	if err != nil {
		t.Errorf("Error extracting Hex from map: %s", err.Error())
	} else if i != fixture["int"] {
		t.Errorf("Error extracting Hex from map: %d != %d", i, fixture["int"])
	}
}

func TestBool(t *testing.T) {
	b, err := Bool(fixture, "bool")
	if err != nil {
		t.Errorf("Error extracting Hex from map: %s", err.Error())
	} else if b != fixture["bool"] {
		t.Errorf("Error extracting Hex from map: %v != %v", b, fixture["bool"])
	}
}
