package flat

import (
	"fmt"
	"testing"
)

func TestParse(t *testing.T) {
	path := []string{"t2"}
	obj := map[string]interface{}{
		"someval": "xyz",
		"b": map[string]interface{}{
			"someval": "xyz",
			"b":       []string{"a", "b", "c"},
			"c":       []int{1, 2, 3},
			"d":       []interface{}{"a", 3, 3.14, []string{"a", "b", "c"}, map[string]interface{}{"d": "new"}},
		},
	}

	items, err := parseInterface(path, obj)
	if err != nil {
		t.Errorf("parse interface failed with: %v", err)
		return
	}

	fmt.Println("ITEMS: ", items)
}
