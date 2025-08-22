package slices

// Contains returns a boolean based on whether a value is contained in the given slice.
func Contains(slice []string, value string) bool {
	for _, s := range slice {
		if value == s {
			return true
		}
	}
	return false
}

// ReverseArray returns the given array in reversed order.
func ReverseArray(arr []string) (reversed []string) {
	reversed = make([]string, len(arr))
	last := len(arr) - 1
	for i, val := range arr {
		reversed[last-i] = val
	}
	return
}

// Last returns the last string in a string slice while avoiding panics from a nil slice.
func Last(arr []string) string {
	if len(arr) == 0 {
		return ""
	}

	return arr[len(arr)-1]
}

// Unique returns a new slice with only unique values from the original slice.
func Unique(strSlice []string) []string {
	var unique []string
	for _, elm := range strSlice {
		if !Contains(unique, elm) {
			unique = append(unique, elm)
		}
	}

	return unique
}
