package maps

import "fmt"

/*
Display displays a map[interface{}]interface{}, map[string]interface{}, []interface{} while also iterating those types within

TODO use constraints
*/
func Display(prefix string, Map interface{}) {
	switch Map.(type) {
	case map[interface{}]interface{}:
		for k, v := range Map.(map[interface{}]interface{}) {
			if !isIterable(v) {
				fmt.Println(prefix, k, ":", v)
			} else {
				fmt.Println(prefix, k, ":")
				Display(prefix+"    ", v)
			}
		}
	case map[string]interface{}:
		for k, v := range Map.(map[string]interface{}) {
			if !isIterable(v) {
				fmt.Println(prefix, k, ":", v)
			} else {
				fmt.Println(prefix, k, ":")
				Display(prefix+"    ", v)
			}
		}
	case []interface{}:
		for k, v := range Map.([]interface{}) {
			if !isIterable(v) {
				fmt.Println(prefix, k, ":", v)
			} else {
				fmt.Println(prefix, k, ":")
				Display(prefix+"    ", v)
			}
		}
	}
}

func isIterable(obj interface{}) bool {
	switch obj.(type) {
	case map[interface{}]interface{}, map[string]interface{}, []interface{}:
		return true
	default:
		return false
	}
}
