package quality

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryBudgetFit(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		budget int64
		wanted int
		cost   int64
		want   int
	}{
		{name: "no budget", budget: 0, wanted: 6, cost: 300, want: 6},
		{name: "room for all", budget: 3000, wanted: 6, cost: 300, want: 6},
		{name: "capped", budget: 1000, wanted: 6, cost: 300, want: 3},
		{name: "at least one", budget: 100, wanted: 6, cost: 300, want: 1},
		{name: "unknown cost", budget: 1000, wanted: 6, cost: 0, want: 6},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, newMemoryBudget(testCase.budget).fit(testCase.wanted, testCase.cost))
		})
	}
}

func TestMemoryBudgetAcquire(
	t *testing.T,
) {
	budget := newMemoryBudget(500)

	first, err := budget.acquire(t.Context(), 300)
	require.NoError(t, err)

	// The second worker does not fit while the first holds its share.
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	_, err = budget.acquire(ctx, 300)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	first()

	second, err := budget.acquire(t.Context(), 300)
	require.NoError(t, err)
	second()

	// A worker above the whole budget runs alone instead of waiting forever.
	alone, err := budget.acquire(t.Context(), 5000)
	require.NoError(t, err)
	alone()

	unlimited, err := memoryBudget{}.acquire(t.Context(), 5000)
	require.NoError(t, err)
	unlimited()
}
