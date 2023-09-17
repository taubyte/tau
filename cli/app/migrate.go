package app

import (
	"context"
	"fmt"
	"os"
)

// migrateDatabase moves the specified shape out of the database.
// It renames the shape and its client (if elder is false) to a new location.
func migrateDatabase(ctx context.Context, shape string, elder bool) error {
	// Rename the shape directory from "/tb/storage/databases/{shape}" to "/tb/storage/{shape}"
	err := os.Rename(fmt.Sprintf("/tb/storage/databases/%s", shape), fmt.Sprintf("/tb/storage/%s", shape))
	if err != nil {
		return fmt.Errorf("migrating %s out of database failed with: %w", shape, err)
	}

	if !elder {
		// If elder is false, rename the shape client directory from "/tb/storage/databases/{shape}_client" to "/tb/storage/{shape}_client"
		err = os.Rename(fmt.Sprintf("/tb/storage/databases/%s_client", shape), fmt.Sprintf("/tb/storage/%s_client", shape))
		return fmt.Errorf("migrating %s_client out of database failed with: %w", shape, err)
	}
	
	return nil // Return nil if the migration is successful
}
