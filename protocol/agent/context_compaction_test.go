package agent

import (
	"github.com/stretchr/testify/require"
	"math"
	"testing"
)

func TestContextCompactionPercentValidation(t *testing.T) {
	require.NoError(t, (&Agent{}).Validate())
	for _, v := range []float64{0, -1, 101, math.NaN(), math.Inf(1), math.Inf(-1)} {
		a := &Agent{ContextCompactionPercent: &v}
		require.Error(t, a.Validate())
	}
	for _, v := range []float64{0.01, 1, 80, 100} {
		a := &Agent{ContextCompactionPercent: &v}
		require.NoError(t, a.Validate())
	}
}
