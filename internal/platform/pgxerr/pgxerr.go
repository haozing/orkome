// Package pgxerr maps PostgreSQL driver errors onto the PRODUCT apierr
// vocabulary. The framework's pg.AsNotFound returns the framework-internal
// apierr type (ploykit/internal/contract), which product writeErr switches
// cannot see (ploykit issue F23) — product repos map FK violations here.
package pgxerr

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"orkome/internal/contract/apierr"
)

// AsNotFound maps a foreign-key violation (23503) to apierr NotFound.
func AsNotFound(err error, msg string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return apierr.New(apierr.NotFound, msg)
	}
	return err
}
