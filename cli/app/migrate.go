package app

import (
	"context"
	"fmt"
	"os"
)

func migrateDatabase(ctx context.Context, shape string, elder bool) error {
	err := os.Rename(fmt.Sprintf("/tb/storage/databases/%s", shape), fmt.Sprintf("/tb/storage/%s", shape))
	if err != nil {
		return fmt.Errorf("migrating %s out of database failed with: %w", shape, err)
	}

	if !elder {
		err = os.Rename(fmt.Sprintf("/tb/storage/databases/%s_client", shape), fmt.Sprintf("/tb/storage/%s_client", shape))
		return fmt.Errorf("migrating %s_client out of database failed with: %w", shape, err)
	}

	return nil
}
