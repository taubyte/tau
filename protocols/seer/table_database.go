package seer

import (
	"database/sql"
)

func initializeTables(db *sql.DB) error {
	create := []string{CreateServiceTable, CreateUsageTable, CreateMetaTable}
	for _, _statement := range create {
		statement, err := db.Prepare(_statement)
		if err != nil {
			return err
		}

		_, err = statement.Exec()
		if err != nil {
			return err
		}

		err = statement.Close()
		if err != nil {
			return err
		}
	}

	return nil
}
