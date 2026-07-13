package sqlite

import (
	"errors"

	sqlitedriver "modernc.org/sqlite"
)

const (
	// sqliteConstraint is the primary SQLite result code SQLITE_CONSTRAINT (a constraint violation).
	sqliteConstraint = 19
	// primaryCodeMask extracts the primary result code from an extended one: the low byte of any
	// extended constraint code (e.g. SQLITE_CONSTRAINT_UNIQUE = 2067 = 19 | (8<<8)) equals
	// SQLITE_CONSTRAINT, so masking matches a constraint violation whether or not the connection has
	// extended result codes enabled.
	primaryCodeMask = 0xff
)

// isConstraintViolation reports whether err is, or wraps, a SQLite constraint-violation error. Used
// to map a unique-index violation to a domain conflict (409) instead of a generic 500.
func isConstraintViolation(err error) bool {
	var serr *sqlitedriver.Error
	return errors.As(err, &serr) && serr.Code()&primaryCodeMask == sqliteConstraint
}
