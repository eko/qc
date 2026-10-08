package quality

import (
	"sync"

	"github.com/eko/qc/frame"
)

// framePools are the frame pools of a Meter, one per frame geometry. Every
// sweep of every measurement draws from them: a pool per sweep left its
// frames to the garbage collector, which a heap of large frames calls so
// rarely that a ladder build kept gigabytes of dead ones. The zero value is
// ready to use.
type framePools struct {
	mu    sync.Mutex
	pools map[poolKey]*frame.Pool
}

type poolKey struct {
	width, height int
	highBitDepth  bool
}

// get returns the pool of width×height 4:2:0 frames at that depth.
func (p *framePools) get(
	width, height int,
	highBitDepth bool,
) *frame.Pool {
	key := poolKey{width: width, height: height, highBitDepth: highBitDepth}

	p.mu.Lock()
	defer p.mu.Unlock()

	pool, ok := p.pools[key]
	if !ok {
		pool = frame.NewPool(width, height, frame.PoolOptions{Chroma: true, HighBitDepth: highBitDepth})

		if p.pools == nil {
			p.pools = map[poolKey]*frame.Pool{}
		}

		p.pools[key] = pool
	}

	return pool
}
