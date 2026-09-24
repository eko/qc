package analysis

import (
	"sync"
	"time"
)

// timings records how long each stage took. Stages of stage 1 run
// concurrently, hence the mutex.
type timings struct {
	mu     sync.Mutex
	stages map[string]time.Duration
}

func newTimings() *timings {
	return &timings{stages: map[string]time.Duration{}}
}

// track starts timing a stage and returns the function that stops it.
func (t *timings) track(
	stage string,
) func() {
	start := time.Now()

	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()

		t.stages[stage] = time.Since(start)
	}
}

// snapshot returns the recorded durations, rounded to the millisecond, as
// strings for the JSON report.
func (t *timings) snapshot() map[string]string {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make(map[string]string, len(t.stages))
	for stage, d := range t.stages {
		out[stage] = d.Round(time.Millisecond).String()
	}

	return out
}
