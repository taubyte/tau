package main

// The database/sql drivers the worker's external engines connect with. A driver
// registers itself from its own init, so it is linked at the composition root and
// nowhere below it: pkg/database and the worker stay driver-free.
import (
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)
