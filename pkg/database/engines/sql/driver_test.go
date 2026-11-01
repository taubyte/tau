package sql_test

import (
	"database/sql"
	"database/sql/driver"
	"io"
	"sync"
)

// A database/sql driver in forty lines, so this package needs no real one. It records
// every DSN it is opened with.

type recorder struct {
	mu   sync.Mutex
	dsns []string
	rows [][]any
	cols []string
	// types are the columns' declared types, as a driver reports them.
	types []string
	fail  error
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
	r.dsns, r.rows, r.cols, r.types, r.fail = nil, nil, nil, nil, nil
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
	return &fakeRows{cols: s.r.cols, types: s.r.types, values: s.r.rows}, nil
}

type fakeResult struct{ affected int64 }

func (f fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (f fakeResult) RowsAffected() (int64, error) { return f.affected, nil }

type fakeRows struct {
	cols   []string
	types  []string
	values [][]any
	at     int
}

func (r *fakeRows) ColumnTypeDatabaseTypeName(i int) string {
	if i < len(r.types) {
		return r.types[i]
	}
	return ""
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
