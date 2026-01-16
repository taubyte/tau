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
