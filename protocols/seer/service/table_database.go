package service

import (
	"database/sql"

	"github.com/taubyte/odo/protocols/seer/common"
)

func initializeTables(db *sql.DB) error {
	create := []string{common.CreateServiceTable, common.CreateUsageTable, common.CreateMetaTable}
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
