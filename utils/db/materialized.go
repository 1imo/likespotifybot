package db

import (
	"database/sql"
	"errors"
	"fmt"
)

// Rows is implemented by *sql.Rows and by MaterializedRows (cached read results).
type Rows interface {
	Close() error
	Columns() ([]string, error)
	Next() bool
	Scan(dest ...interface{}) error
	Err() error
}

// Row is implemented by *sql.Row and by materializedRow (cached single-row reads).
type Row interface {
	Scan(dest ...interface{}) error
	Err() error
}

// MaterializedRows replays column data without hitting the database (used for cache hits).
type MaterializedRows struct {
	cols []string
	data [][]interface{}
	idx  int
	err  error
}

func newMaterializedRows(cols []string, data [][]interface{}) *MaterializedRows {
	return &MaterializedRows{cols: cols, data: data, idx: -1}
}

func (m *MaterializedRows) Close() error {
	return nil
}

func (m *MaterializedRows) Columns() ([]string, error) {
	return m.cols, nil
}

func (m *MaterializedRows) Next() bool {
	if m.err != nil {
		return false
	}
	m.idx++
	return m.idx < len(m.data)
}

func (m *MaterializedRows) Scan(dest ...interface{}) error {
	if m.idx < 0 || m.idx >= len(m.data) {
		return fmt.Errorf("sql: Scan called without calling Next")
	}
	return scanRowValues(m.data[m.idx], dest...)
}

func (m *MaterializedRows) Err() error {
	return m.err
}

type materializedRow struct {
	vals []interface{}
	err  error
	done bool
}

func (r *materializedRow) Scan(dest ...interface{}) error {
	if r.err != nil {
		return r.err
	}
	if r.done {
		return errors.New("sql: Scan called twice on the same Row")
	}
	r.done = true
	return scanRowValues(r.vals, dest...)
}

func (r *materializedRow) Err() error {
	return r.err
}

func drainSQLRows(rows *sql.Rows) (cols []string, data [][]interface{}, err error) {
	cols, err = rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		ptrs := make([]interface{}, len(cols))
		for i := range ptrs {
			ptrs[i] = new(interface{})
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		row := make([]interface{}, len(cols))
		for i := range row {
			row[i] = cloneIface(*(ptrs[i].(*interface{})))
		}
		data = append(data, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return cols, data, nil
}

func cloneIface(v interface{}) interface{} {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		b := make([]byte, len(t))
		copy(b, t)
		return b
	default:
		return t
	}
}

func cloneRowsData(data [][]interface{}) [][]interface{} {
	out := make([][]interface{}, len(data))
	for i, row := range data {
		out[i] = make([]interface{}, len(row))
		for j, v := range row {
			out[i][j] = cloneIface(v)
		}
	}
	return out
}

func cloneRowVals(vals []interface{}) []interface{} {
	out := make([]interface{}, len(vals))
	for i, v := range vals {
		out[i] = cloneIface(v)
	}
	return out
}

func scanRowValues(vals []interface{}, dest ...interface{}) error {
	if len(vals) != len(dest) {
		return fmt.Errorf("sql: expected %d destination arguments in Scan, got %d", len(vals), len(dest))
	}
	for i := range dest {
		if err := assignScan(dest[i], vals[i]); err != nil {
			return err
		}
	}
	return nil
}
