package maps

import (
	"errors"
	"fmt"

	"github.com/taubyte/tau/utils/hex"
)

// Number retrieves the interface{} value from the map and returns a float32.
func Number(config map[string]interface{}, key string) (float32, error) {
	var ok bool
	if _, ok = config[key]; ok == false {
		return 0, errors.New("You must define `" + key + "`")
	} else {
		val := config[key]
		switch val.(type) {
		case int:
			return float32(val.(int)), nil
		case int16:
			return float32(val.(int16)), nil
		case int32:
			return float32(val.(int32)), nil
		case int64:
			return float32(val.(int64)), nil
		case uint:
			return float32(val.(uint)), nil
		case uint16:
			return float32(val.(uint16)), nil
		case uint32:
			return float32(val.(uint32)), nil
		case uint64:
			return float32(val.(uint64)), nil
		case float32:
			return val.(float32), nil
		case float64:
			return float32(val.(float64)), nil
		default:
			return 0, fmt.Errorf("`%s` needs to be a Number not a %T", val, val)
		}
	}
}

// Vector retrieves a interface{} value from the map and returns a vector.
func Vector(config map[string]interface{}, key string) ([]float32, error) {
	var ok bool
	if _, ok = config[key]; ok == false {
		return nil, errors.New("You must define `" + key + "`")
	} else {
		val := config[key]
		switch val.(type) {
		case []int:
			array := val.([]int)
			ret := make([]float32, len(array))
			for i, v := range array {
				ret[i] = float32(v)
			}
			return ret, nil
		case []int16:
			array := val.([]int16)
			ret := make([]float32, len(array))
			for i, v := range array {
				ret[i] = float32(v)
			}
			return ret, nil
		case []int32:
			array := val.([]int32)
			ret := make([]float32, len(array))
			for i, v := range array {
				ret[i] = float32(v)
			}
			return ret, nil
		case []int64:
			array := val.([]int64)
			ret := make([]float32, len(array))
			for i, v := range array {
				ret[i] = float32(v)
			}
			return ret, nil
		case []uint:
			array := val.([]uint)
			ret := make([]float32, len(array))
			for i, v := range array {
				ret[i] = float32(v)
			}
			return ret, nil
		case []uint16:
			array := val.([]uint16)
			ret := make([]float32, len(array))
			for i, v := range array {
				ret[i] = float32(v)
			}
			return ret, nil
		case []uint32:
			array := val.([]uint32)
			ret := make([]float32, len(array))
			for i, v := range array {
				ret[i] = float32(v)
			}
			return ret, nil
		case []uint64:
			array := val.([]uint64)
			ret := make([]float32, len(array))
			for i, v := range array {
				ret[i] = float32(v)
			}
			return ret, nil
		case []float32:
			return val.([]float32), nil
		case float64:
			array := val.([]float64)
			ret := make([]float32, len(array))
			for i, v := range array {
				ret[i] = float32(v)
			}
			return ret, nil
		default:
			return nil, fmt.Errorf("`%s` needs to be a Vector not a %T", val, val)
		}
	}
}

// String retrieves interface{} value from map and returns a string value.
func String(config map[string]interface{}, key string) (string, error) {
	if _, ok := config[key]; !ok {
		return "", errors.New("you must define `" + key + "`")
	} else {
		switch config[key].(type) {
		case string:
			return config[key].(string), nil
		default:
			return "", errors.New("`" + key + "` needs to be a String")
		}
	}
}

func TryString(config map[string]interface{}, key string) string {
	str, err := String(config, key)
	if err != nil {
		return ""
	}
	return str
}

// ByteArray retrieves the interface{} value from the map and returns a byte array.
func ByteArray(config map[string]interface{}, key string) ([]byte, error) {
	var ret []byte
	if _, ok := config[key]; !ok {
		return ret, errors.New("you must define `" + key + "`")
	} else {
		switch config[key].(type) {
		case []byte:
			ret = config[key].([]byte)
		default:
			return ret, errors.New("`" + key + "` needs to be a []byte")
		}
	}
	return ret, nil
}

// StringArray retrieves the interface{} value from the map and returns a string slice.
func StringArray(config map[string]interface{}, key string) ([]string, error) {
	var ok bool
	var ret []string
	if _, ok = config[key]; !ok {
		return ret, errors.New("You must define `" + key + "`")
	} else {
		switch config[key].(type) {
		case []string:
			ret = config[key].([]string)
		case []interface{}:
			for _, v := range config[key].([]interface{}) {
				switch v.(type) {
				case string:
					ret = append(ret, v.(string))
				default:
					return ret, errors.New(fmt.Sprintf("`%s` with element %T can not be converted to []string", key, config[key]))
				}
			}
		default:
			return ret, errors.New(fmt.Sprintf("`%s` needs to be a []string not %T", key, config[key]))
		}
	}
	return ret, nil
}

// HexInt retrieves the interface{} value from the map and returns an int.
func HexInt(config map[string]interface{}, key string) (int, error) {
	hexString, err := String(config, key)
	if err != nil {
		return 0, err
	}
	hex, err := hex.Int(hexString)
	if err != nil {
		return 0, err
	}
	return int(hex), nil
}

func Int(config map[string]interface{}, key string) (int, error) {
	var ok bool
	var ret int
	if _, ok = config[key]; ok == false {
		return ret, errors.New("You must define `" + key + "`")
	} else {
		switch config[key].(type) {
		case int:
			ret = config[key].(int)
		case uint:
			ret = int(config[key].(uint))
		case uint64:
			ret = int(config[key].(uint64))
		case int64:
			ret = int(config[key].(int64))
		case uint32:
			ret = int(config[key].(uint32))
		case int32:
			ret = int(config[key].(int32))
		case uint8:
			ret = int(config[key].(uint8))
		case int8:
			ret = int(config[key].(int8))
		default:
			return ret, errors.New(fmt.Sprintf("`%s` needs to be a Int not a %T. Provided: %v", key, config[key], config[key]))
		}
	}
	return ret, nil
}

// Bool retrieves the interface{} value from the map and returns a boolean.
func Bool(config map[string]interface{}, key string) (bool, error) {
	var ok bool
	var ret bool
	if _, ok = config[key]; ok == false {
		return ret, errors.New("You must define `" + key + "`")
	} else {
		switch config[key].(type) {
		case bool:
			ret = config[key].(bool)
		default:
			return ret, errors.New(fmt.Sprintf("`%s` needs to be a Int. Provided: %v", key, config[key]))
		}
	}
	return ret, nil
}

// Keys returns all string keys in the given map.
func Keys(m map[string]interface{}) []string {
	if m == nil {
		return nil
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	return keys
}

// IntKeys returns all int keys in the given map.
func IntKeys(m map[int]interface{}) []int {
	if m == nil {
		return nil
	}

	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	return keys
}

// SafeStringKeys converts a map[interface{}]interface{} to a map[string]interface{},
// if key is not a string value then key is converted to a string.
func SafeStringKeys(m map[interface{}]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}

	r := make(map[string]interface{}, len(m))
	for k, v := range m {
		switch k.(type) {
		case string:
			r[k.(string)] = v
		default:
			r[fmt.Sprint(k)] = v
		}
	}

	return r
}

// ToStringKeys converts a map[interface{}]interface{} to a map[string]interface{},
// returns an error if key is not a string.
func ToStringKeys(m map[interface{}]interface{}) (map[string]interface{}, error) {
	if m == nil {
		return nil, nil
	}

	r := make(map[string]interface{}, len(m))
	for k, v := range m {
		switch k.(type) {
		case string:
			r[k.(string)] = v
		default:
			return nil, errors.New(fmt.Sprintf("Can't convert key `%v` of type %T intro string", k, k))
		}
	}

	return r, nil
}

// InterfaceToStringKeys converts an interface{} to a map[string]interface{},
// returns an error if key is not a string.
func InterfaceToStringKeys(m interface{}) (map[string]interface{}, error) {
	if m == nil {
		return nil, nil
	}

	if m0, ok := m.(map[string]interface{}); ok == true {
		return m0, nil
	}

	if m0, ok := m.(map[interface{}]interface{}); ok == true {
		return ToStringKeys(m0)
	} else {
		return nil, errors.New(fmt.Sprintf("Can't convet %T to a map", m))
	}
}

// SafeInterfaceToStringKeys converts an interface to map[string]interface{},
// if keys are not strings, then the key is converted into a string.
func SafeInterfaceToStringKeys(m interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}

	if m0, ok := m.(map[string]interface{}); ok == true {
		return m0
	}

	if m0, ok := m.(map[interface{}]interface{}); ok == true {
		return SafeStringKeys(m0)
	} else {
		return nil
	}
}
