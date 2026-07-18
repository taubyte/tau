package utils

import (
	"fmt"

	"github.com/alecthomas/units"
	"github.com/taubyte/tau/pkg/tcc/object"
)

// ParseSize parses a size string from the specified field and sets it as bytes (int64).
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

// FormatSize converts bytes (int64) back to a human-readable size string.
// If the field doesn't exist or is nil, the function returns nil (no error).
// Returns an error if the field exists but is not an int64 or cannot be formatted.
func FormatSize(sel object.Selector[object.Refrence], field string) error {
	size, err := sel.Get(field)
	if err != nil {
		// Field doesn't exist, which is fine
		return nil
	}

	// Field exists but is nil, which is also fine
	if size == nil {
		return nil
	}

	var sizeBytes int64
	switch v := size.(type) {
	case int64:
		sizeBytes = v
	case int:
		sizeBytes = int64(v)
	case int32:
		sizeBytes = int64(v)
	default:
		return fmt.Errorf("%s is not an integer", field)
	}

	sizeStr := units.MetricBytes(float64(sizeBytes)).String()
	return sel.Set(field, sizeStr)
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
