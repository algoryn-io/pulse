package pulse

import (
	"math/rand/v2"
	"sync"
	"sync/atomic"
)

// activePool is the current pool of goroutine-local *rand.Rand instances used
// in the default (unseeded) path for lock-free performance under concurrency.
var activePool atomic.Pointer[sync.Pool]

// seeded records whether SetSeed has been called. Used by RunContext to
// prevent double-seeding when Config.Seed is set but SetSeed was already
// called by the caller.
var seeded atomic.Bool

// seededMu and seededRng protect the single shared RNG used when a seed is
// active. A mutex-serialised single source is necessary for reproducibility:
// sync.Pool can evict items between GC cycles, causing a pool-based approach
// to produce different RNG instances at different points in the sequence.
var seededMu  sync.Mutex
var seededRng *rand.Rand

func init() {
	activePool.Store(newPool(defaultFactory()))
}

// defaultFactory returns a pool New function that seeds each new RNG from
// the package-level random source, producing unique streams across goroutines.
func defaultFactory() func() any {
	return func() any {
		return rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}
}

func newPool(factory func() any) *sync.Pool {
	return &sync.Pool{New: factory}
}

// SetSeed sets a deterministic seed for the random source used by built-in
// middlewares (WithErrorRate, WithJitter, WithLatency, WithStatusCode).
//
// After SetSeed is called all middleware RNG calls share a single
// mutex-serialised source derived from seed. This guarantees that two runs
// with the same seed and the same sequential call order produce identical
// injected-fault patterns. Because OS scheduling can vary which goroutine
// draws which random value under concurrency, exact replay is best-effort.
func SetSeed(seed int64) {
	seededMu.Lock()
	seededRng = rand.New(rand.NewPCG(uint64(seed), uint64(seed)^0x9e3779b97f4a7c15))
	seededMu.Unlock()
	seeded.Store(true)
}

// hasSeed reports whether SetSeed has been called.
func hasSeed() bool {
	return seeded.Load()
}

// randomFloat64 returns a uniformly distributed float64 in [0.0, 1.0).
// When a seed is active it uses the shared seeded RNG; otherwise it checks
// out a goroutine-local RNG from the pool for lock-free operation.
func randomFloat64() float64 {
	if seeded.Load() {
		seededMu.Lock()
		v := seededRng.Float64()
		seededMu.Unlock()
		return v
	}
	pool := activePool.Load()
	r := pool.Get().(*rand.Rand)
	v := r.Float64()
	pool.Put(r)
	return v
}

// randomInt64N returns a non-negative pseudo-random int64 in [0, n).
// When a seed is active it uses the shared seeded RNG; otherwise it checks
// out a goroutine-local RNG from the pool for lock-free operation.
func randomInt64N(n int64) int64 {
	if seeded.Load() {
		seededMu.Lock()
		v := seededRng.Int64N(n)
		seededMu.Unlock()
		return v
	}
	pool := activePool.Load()
	r := pool.Get().(*rand.Rand)
	v := r.Int64N(n)
	pool.Put(r)
	return v
}
