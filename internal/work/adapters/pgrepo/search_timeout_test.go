package pgrepo

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
)

func TestMapSearchTimeout(t *testing.T) {

	timeout := &pgconn.PgError{Code: "57014", Message: "canceling statement due to statement timeout"}
	for _, err := range []error{
		timeout,
		fmt.Errorf("search rows: %w", timeout),
		fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", timeout)),
	} {
		mapped := MapSearchTimeout(err)
		ae, ok := mapped.(*apierr.Error)
		require.True(t, ok, "err=%v", err)
		assert.Equal(t, apierr.SearchTimeout, ae.Code)
	}

	other := &pgconn.PgError{Code: "23505", Message: "duplicate key"}
	assert.Same(t, other, MapSearchTimeout(other))
	plain := errors.New("boom")
	assert.Same(t, plain, MapSearchTimeout(plain))
	assert.Nil(t, MapSearchTimeout(nil))
}
