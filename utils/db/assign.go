package db

import (
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"time"
)

// assignScan copies a driver value from src into dest (a pointer), covering common PostgreSQL / lib/pq types.
func assignScan(dest, src interface{}) error {
	if dest == nil {
		return fmt.Errorf("destination is nil")
	}

	if scanner, ok := dest.(sql.Scanner); ok {
		return scanner.Scan(src)
	}

	switch d := dest.(type) {
	case *string:
		if d == nil {
			return errNilDest
		}
		switch s := src.(type) {
		case nil:
			*d = ""
		case string:
			*d = s
		case []byte:
			*d = string(s)
		default:
			*d = fmt.Sprint(s)
		}
		return nil

	case *[]byte:
		if d == nil {
			return errNilDest
		}
		switch s := src.(type) {
		case nil:
			*d = nil
		case []byte:
			b := make([]byte, len(s))
			copy(b, s)
			*d = b
		case string:
			*d = []byte(s)
		default:
			return fmt.Errorf("cannot scan %T into *[]byte", src)
		}
		return nil

	case *int:
		if d == nil {
			return errNilDest
		}
		n, err := asInt64(src)
		if err != nil {
			return err
		}
		*d = int(n)
		return nil

	case *int64:
		if d == nil {
			return errNilDest
		}
		n, err := asInt64(src)
		if err != nil {
			return err
		}
		*d = n
		return nil

	case *bool:
		if d == nil {
			return errNilDest
		}
		switch s := src.(type) {
		case nil:
			*d = false
		case bool:
			*d = s
		case int64:
			*d = s != 0
		case []byte:
			*d = len(s) > 0 && s[0] == 't'
		case string:
			*d = s == "true" || s == "t" || s == "1"
		default:
			return fmt.Errorf("cannot scan %T into *bool", src)
		}
		return nil

	case *time.Time:
		if d == nil {
			return errNilDest
		}
		switch s := src.(type) {
		case nil:
			*d = time.Time{}
		case time.Time:
			*d = s
		case []byte:
			t, err := time.Parse(time.RFC3339Nano, string(s))
			if err != nil {
				t, err = time.Parse("2006-01-02 15:04:05.999999999-07", string(s))
			}
			if err != nil {
				return err
			}
			*d = t
		case string:
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				t, err = time.Parse("2006-01-02 15:04:05.999999999-07", s)
			}
			if err != nil {
				return err
			}
			*d = t
		default:
			return fmt.Errorf("cannot scan %T into *time.Time", src)
		}
		return nil
	}

	dpv := reflect.ValueOf(dest)
	if dpv.Kind() != reflect.Ptr || dpv.IsNil() {
		return fmt.Errorf("destination must be non-nil pointer")
	}
	sv := reflect.ValueOf(src)
	if !sv.IsValid() {
		return nil
	}
	dv := reflect.Indirect(dpv)
	if sv.Type().AssignableTo(dv.Type()) {
		dv.Set(sv)
		return nil
	}
	return fmt.Errorf("unsupported Scan destination %T from %T", dest, src)
}

var errNilDest = fmt.Errorf("destination pointer is nil")

func asInt64(src interface{}) (int64, error) {
	switch s := src.(type) {
	case nil:
		return 0, nil
	case int64:
		return s, nil
	case int:
		return int64(s), nil
	case int32:
		return int64(s), nil
	case uint64:
		return int64(s), nil
	case []byte:
		return strconv.ParseInt(string(s), 10, 64)
	case string:
		return strconv.ParseInt(s, 10, 64)
	case float64:
		return int64(s), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to int64", src)
	}
}
