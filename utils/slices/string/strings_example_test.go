package slices_test

import (
	"fmt"

	slices "github.com/taubyte/tau/utils/slices/string"
)

func ExampleUnique() {
	s := []string{"one", "two", "three", "two"}
	s = slices.Unique(s)

	fmt.Println(s)

	// Output: [one two three]
}
