package utils

import (
	"fmt"
	"time"

	"github.com/alecthomas/units"
	"github.com/taubyte/tau/pkg/tcc/object"
)

// ParseTimeout parses a duration string from the specified field and sets it as nanoseconds.
// If the field doesn't exist or is nil, the function returns nil (no error).
// Returns an error if the field exists but is not a string or cannot be parsed.
func ParseTimeout(sel object.Selector[object.Refrence], field string) error {
	timeout, err := sel.Get(field)
	if err != nil {
		// Field doesn't exist, which is fine
		return nil
	}

	// Field exists but is nil, which is also fine
	if timeout == nil {
		return nil
	}

	timeoutStr, ok := timeout.(string)
	if !ok {
		return fmt.Errorf("%s is not a string", field)
	}

	timeoutDur, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return fmt.Errorf("parsing %s failed with %w", field, err)
	}

	return sel.Set(field, timeoutDur.Nanoseconds())
}

// ParseMemory parses a memory size string from the specified field and sets it as bytes (int64).
// If the field doesn't exist or is nil, the function returns nil (no error).
// Returns an error if the field exists but is not a string or cannot be parsed.
func ParseMemory(sel object.Selector[object.Refrence], field string) error {
	memory, err := sel.Get(field)
	if err != nil {
		// Field doesn't exist, which is fine
		return nil
	}

	// Field exists but is nil, which is also fine
	if memory == nil {
		return nil
	}

	memoryStr, ok := memory.(string)
	if !ok {
		return fmt.Errorf("%s is not a string", field)
	}

	memoryInt, err := units.ParseStrictBytes(memoryStr)
	if err != nil {
		return fmt.Errorf("parsing %s failed with %w", field, err)
	}

	return sel.Set(field, memoryInt)
}

// ParseSize parses a size string from the specified field and sets it as bytes (int64).
// This is an alias for ParseMemory for semantic clarity.
// If the field doesn't exist or is nil, the function returns nil (no error).
// Returns an error if the field exists but is not a string or cannot be parsed.
func ParseSize(sel object.Selector[object.Refrence], field string) error {
	size, err := sel.Get(field)
	if err != nil {
		// Field doesn't exist, which is fine
		return nil
	}

	// Some code checks for nil explicitly, so we handle that too
	if size == nil {
		return nil
	}

	sizeStr, ok := size.(string)
	if !ok {
		return fmt.Errorf("%s is not a string", field)
	}

	sizeInt, err := units.ParseStrictBytes(sizeStr)
	if err != nil {
		return fmt.Errorf("parsing %s failed with %w", field, err)
	}

	return sel.Set(field, sizeInt)
}

// RenameById extracts the "id" field, sets "name" to the provided name,
// deletes the "id" field, and renames the selector to use the id value.
// Returns an error if the id field doesn't exist or is not a string.
func RenameById(sel object.Selector[object.Refrence], name string) (string, error) {
	id, err := sel.Get("id")
	if err != nil {
		return "", fmt.Errorf("fetching id failed with %w", err)
	}

	idStr, ok := id.(string)
	if !ok {
		return "", fmt.Errorf("id is not a string")
	}

	if err := sel.Set("name", name); err != nil {
		return "", fmt.Errorf("setting name failed with %w", err)
	}

	sel.Delete("id")

	if err := sel.Rename(idStr); err != nil {
		return "", fmt.Errorf("renaming to id failed with %w", err)
	}

	return idStr, nil
}

// FormatTimeout converts nanoseconds back to duration string.
// If the field doesn't exist or is nil, the function returns nil (no error).
// Returns an error if the field exists but is not an int64 or cannot be formatted.
func FormatTimeout(sel object.Selector[object.Refrence], field string) error {
	timeout, err := sel.Get(field)
	if err != nil {
		// Field doesn't exist, which is fine
		return nil
	}

	// Field exists but is nil, which is also fine
	if timeout == nil {
		return nil
	}

	var timeoutNs int64
	switch v := timeout.(type) {
	case int64:
		timeoutNs = v
	case int:
		timeoutNs = int64(v)
	case int32:
		timeoutNs = int64(v)
	default:
		return fmt.Errorf("%s is not an integer", field)
	}

	timeoutDur := time.Duration(timeoutNs)
	return sel.Set(field, timeoutDur.String())
}

// FormatMemory converts bytes (int64) back to human-readable size string.
// If the field doesn't exist or is nil, the function returns nil (no error).
// Returns an error if the field exists but is not an int64 or cannot be formatted.
func FormatMemory(sel object.Selector[object.Refrence], field string) error {
	memory, err := sel.Get(field)
	if err != nil {
		// Field doesn't exist, which is fine
		return nil
	}

	// Field exists but is nil, which is also fine
	if memory == nil {
		return nil
	}

	var memoryBytes int64
	switch v := memory.(type) {
	case int64:
		memoryBytes = v
	case int:
		memoryBytes = int64(v)
	case int32:
		memoryBytes = int64(v)
	default:
		return fmt.Errorf("%s is not an integer", field)
	}

	memoryStr := units.MetricBytes(float64(memoryBytes)).String()
	return sel.Set(field, memoryStr)
}

// FormatSize converts bytes (int64) back to human-readable size string.
// This is an alias for FormatMemory for semantic clarity.
// If the field doesn't exist or is nil, the function returns nil (no error).
// Returns an error if the field exists but is not an int64 or cannot be formatted.
func FormatSize(sel object.Selector[object.Refrence], field string) error {
	return FormatMemory(sel, field)
}

// RenameByName reverses RenameById - swaps ID/name back.
// Extracts the "name" field, sets "id" to the current key,
// deletes the "name" field, and renames the selector to use the name value.
// Returns the original name and an error if the name field doesn't exist or is not a string.
func RenameByName(sel object.Selector[object.Refrence]) (string, error) {
	name, err := sel.Get("name")
	if err != nil {
		return "", fmt.Errorf("fetching name failed with %w", err)
	}

	nameStr, ok := name.(string)
	if !ok {
		return "", fmt.Errorf("name is not a string")
	}

	currentKey := sel.Name()
	if currentKey == "" {
		return "", fmt.Errorf("current key is empty")
	}

	if err := sel.Set("id", currentKey); err != nil {
		return "", fmt.Errorf("setting id failed with %w", err)
	}

	sel.Delete("name")

	if err := sel.Rename(nameStr); err != nil {
		return "", fmt.Errorf("renaming to name failed with %w", err)
	}

	return nameStr, nil
}
