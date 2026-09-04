package sql_test

import (
	"database/sql"
	"database/sql/driver"
	"io"
	"sync"
)

// driver_test.go is a database/sql driver in forty lines.
//
// IT EXISTS SO THIS PACKAGE NEEDS NO REAL ONE. What the engine does is build a
// connection string and wrap a pool, and both are testable against anything that
// registers itself — so the test registers something rather than the repository
// depending on Postgres.
//
// It records every DSN it was opened with, which is how the tests below check a
// connection string without a server to send it to.

type recorder struct {
	mu   sync.Mutex
	dsns []string
	rows [][]any
	cols []string
	fail error
}

var registry = &recorder{}

func init() { sql.Register("recorder", registry) }

func (r *recorder) Open(dsn string) (driver.Conn, error) {
	r.mu.Lock()
	r.dsns = append(r.dsns, dsn)
	err := r.fail
	r.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return &fakeConn{r: r}, nil
}

func (r *recorder) opened() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.dsns...)
}

func (r *recorder) reset() {
	r.mu.Lock()
	r.dsns, r.rows, r.cols, r.fail = nil, nil, nil, nil
	r.mu.Unlock()
}

func (r *recorder) answer(cols []string, values ...[]any) {
	r.mu.Lock()
	r.cols, r.rows = cols, values
	r.mu.Unlock()
}

type fakeConn struct{ r *recorder }

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) { return &fakeStmt{r: c.r}, nil }
func (c *fakeConn) Close() error                              { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)                 { return nil, driver.ErrSkip }

type fakeStmt struct{ r *recorder }

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }

func (s *fakeStmt) Exec([]driver.Value) (driver.Result, error) { return fakeResult{affected: 3}, nil }

func (s *fakeStmt) Query([]driver.Value) (driver.Rows, error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	return &fakeRows{cols: s.r.cols, values: s.r.rows}, nil
}

type fakeResult struct{ affected int64 }

func (f fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (f fakeResult) RowsAffected() (int64, error) { return f.affected, nil }

type fakeRows struct {
	cols   []string
	values [][]any
	at     int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }

func (r *fakeRows) Next(dest []driver.Value) error {
	if r.at >= len(r.values) {
		return io.EOF
	}
	for i, v := range r.values[r.at] {
		if i < len(dest) {
			dest[i] = v
		}
	}
	r.at++
	return nil
}
