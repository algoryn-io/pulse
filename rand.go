package pulse

import (
	"math/rand"
	"sync"
	"time"
)

var (
	rngMu  sync.Mutex
	rng    = rand.New(rand.NewSource(time.Now().UnixNano()))
	seeded bool
)

// SetSeed replaces the random source used by built-in middlewares.
func SetSeed(seed int64) {
	rngMu.Lock()
	rng = rand.New(rand.NewSource(seed))
	seeded = true
	rngMu.Unlock()
}

func hasSeed() bool {
	rngMu.Lock()
	defer rngMu.Unlock()
	return seeded
}

func randomFloat64() float64 {
	rngMu.Lock()
	defer rngMu.Unlock()
	return rng.Float64()
}

func randomInt63n(n int64) int64 {
	rngMu.Lock()
	defer rngMu.Unlock()
	return rng.Int63n(n)
}
