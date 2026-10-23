package cputime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMeters(
	t *testing.T,
) {
	outer, inner := &Meter{}, &Meter{}
	ctx := With(context.Background(), outer)
	nested := With(ctx, inner)

	var wg sync.WaitGroup

	for range 10 {
		wg.Go(func() { Add(nested, 100*time.Millisecond) })
	}

	wg.Wait()
	Add(ctx, time.Second)
	Add(context.Background(), time.Hour)

	assert.InDelta(t, 1.0, inner.Seconds(), 1e-9, "the work under it")
	assert.InDelta(t, 2.0, outer.Seconds(), 1e-9, "the nested work and its own")
}
