package app

import (
	"fmt"

	seer "github.com/taubyte/tau/pkg/yaseer"
)

// MIGRATION from Protocols to Services
func configMigration(fs seer.Option, shape string) error {
	migrate, err := seer.New(fs)
	if err != nil {
		return fmt.Errorf("reading config folder failed with %w", err)
	}

	var val []string
	err = migrate.Get(shape).Get("protocols").Value(&val)
	if err == nil {
		err = migrate.Get(shape).Get("protocols").Delete().Commit()
		if err != nil {
			return fmt.Errorf("migration failed deleting `protocols` with %w", err)
		}

		fmt.Println(val)
		if len(val) != 0 {
			err = migrate.Get(shape).Get("services").Set(val).Commit()
			if err != nil {
				return fmt.Errorf("updating `services` failed with %w", err)
			}
		}
	}

	return migrate.Sync()
}
