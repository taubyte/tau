package helpers

import (
	"encoding/json"

	"github.com/jedib0t/go-pretty/v6/table"
)

// https://github.com/go-testfixtures/testfixtures/blob/master/json.go
func appendMapInterface(t table.Writer, iface interface{}, level int, key string) {
	switch iface.(type) {
	case map[interface{}]interface{}:
		for _key, _iface := range iface.(map[interface{}]interface{}) {
			t.AppendRow([]interface{}{_key})
			appendMapInterface(t, _iface, level+1, _key.(string))
		}
	case interface{}:
		empty := make([]interface{}, level)
		for idx := range empty {
			empty[idx] = ""
			if idx == level {
				break
			}
		}
		empty = append(empty, key)
		empty = append(empty, iface)
		t.AppendRow(empty)
		t.AppendSeparator()
	}
}

type jsonArray []interface{}
type jsonMap map[string]interface{}

func (m jsonMap) Value() (interface{}, error) {
	return json.Marshal(m)
}

// Go refuses to convert map[interface{}]interface{} to JSON because JSON only support string keys
// So it's necessary to recursively convert all map[interface]interface{} to map[string]interface{}
func recursiveToJSON(v interface{}) (r interface{}) {
	switch v := v.(type) {
	case []interface{}:
		for i, e := range v {
			v[i] = recursiveToJSON(e)
		}
		r = jsonArray(v)
	case map[interface{}]interface{}:
		newMap := make(map[string]interface{}, len(v))
		for k, e := range v {
			newMap[k.(string)] = recursiveToJSON(e)
		}
		r = jsonMap(newMap)
	default:
		r = v
	}
	return
}
