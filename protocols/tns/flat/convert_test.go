package flat

import (
	"reflect"
	"testing"
)

func TestConvert(t *testing.T) {
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

	flatObj, err := New(path, obj)
	if err != nil {
		t.Errorf("parse interface failed with: %v", err)
		return
	}

	if !reflect.DeepEqual(obj, flatObj.Interface()) {
		t.Error("Objects do not match")
	}

}
