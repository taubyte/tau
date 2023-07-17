package common

func ReverseArray(arr []string) (reversed []string) {
	reversed = make([]string, len(arr))
	last := len(arr) - 1
	for i, val := range arr {
		reversed[last-i] = val
	}
	return
}
