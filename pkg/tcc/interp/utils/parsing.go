package utils

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
)

// RenameById extracts the "id" field, sets "name" to the provided name,
// deletes the "id" field, and renames the selector to use the id value.
// Returns an error if the id field doesn't exist or is not a string.
func RenameById(sel object.Selector[object.Refrence], name, idField, nameField string) (string, error) {
	id, err := sel.Get(idField)
	if err != nil {
		return "", fmt.Errorf("fetching id failed with %w", err)
	}

	idStr, ok := id.(string)
	if !ok {
		return "", fmt.Errorf("id is not a string")
	}

	if err := sel.Set(nameField, name); err != nil {
		return "", fmt.Errorf("setting name failed with %w", err)
	}

	sel.Delete(idField)

	if err := sel.Rename(idStr); err != nil {
		return "", fmt.Errorf("renaming to id failed with %w", err)
	}

	return idStr, nil
}

// RenameByName reverses RenameById: the resource goes back to being keyed by its name,
// with its id back in the field it was authored in. Both field names come from the
// schema (engine.Identity) — the same pair the forward direction was given.
func RenameByName(sel object.Selector[object.Refrence], idField, nameField string) (string, error) {
	name, err := sel.Get(nameField)
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

	if err := sel.Set(idField, currentKey); err != nil {
		return "", fmt.Errorf("setting id failed with %w", err)
	}

	sel.Delete(nameField)

	if err := sel.Rename(nameStr); err != nil {
		return "", fmt.Errorf("renaming to name failed with %w", err)
	}

	return nameStr, nil
}
