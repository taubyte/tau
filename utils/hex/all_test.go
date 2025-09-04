package hex

import (
	"fmt"
	"testing"
)

func TestStrip(t *testing.T) {
	a := "0xFFFF"
	b := "0XFFFF"
	if Strip(a) != Strip(b) {
		t.Errorf("Failed to strip string: %s != %s", Strip(a), Strip(b))
	}
}

func TestInt(t *testing.T) {
	var i int64
	for i = 0; i < 0xffff; i++ {
		h := fmt.Sprintf("%X", i)
		_i, err := Int(h)
		if err != nil {
			t.Errorf("Failed to convert to int: %s", err.Error())
			break
		}
		if _i != i {
			t.Errorf("Failed to convert `%s` to int: %d != %d", h, i, _i)
			break
		}
	}
}
