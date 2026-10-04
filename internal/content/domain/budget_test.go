package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
)

func TestValidateSourceInputBoundaries(t *testing.T) {

	require.NoError(t, ValidateSourceInput(72<<10, 64<<10))
	require.NoError(t, ValidateSourceInput(0, 0))

	err := ValidateSourceInput(72<<10+1, 64<<10)
	require.Error(t, err)
	ae, ok := err.(*apierr.Error)
	require.True(t, ok, "over-budget returns apierr")
	assert.Equal(t, apierr.Validation, ae.Code)

	err = ValidateSourceInput(72<<10, 64<<10+1)
	require.Error(t, err)
	ae, _ = err.(*apierr.Error)
	assert.Equal(t, apierr.Validation, ae.Code)

	require.Error(t, ValidateSourceInput(70<<10, 64<<10+1))
}

func TestValidateHostQuotaBoundaries(t *testing.T) {
	require.NoError(t, ValidateHostQuota(99, 0, 1))
	require.Error(t, ValidateHostQuota(100, 0, 1), "the 101st rejected")
	require.NoError(t, ValidateHostQuota(0, 500<<20, 0))
	require.Error(t, ValidateHostQuota(0, 500<<20-1, 2), "  1 byte over rejected")
	require.NoError(t, ValidateHostQuota(99, 500<<20-1, 1))
}
