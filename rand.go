package pulse

import (
	"math/rand/v2"
	"sync"
	"sync/atomic"
)

// activePool is the current pool of goroutine-local *rand.Rand instances.
// It is replaced atomically by SetSeed so that old (pre-seed) RNGs are
// abandoned and only fresh, deterministically-seeded ones are issued.
var activePool atomic.Pointer[sync.Pool]

// seeded records whether SetSeed has been called. Used by RunContext to
// prevent double-seeding when Config.Seed is set but SetSeed was already
// called by the caller.
var seeded atomic.Bool

func init() {
	p := newPool(defaultFactory())
	activePool.Store(p)
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
// Each call replaces the active pool so that any RNGs allocated after this
// point derive from seed. Previously checked-out RNGs are abandoned with the
// old pool and eventually garbage-collected.
//
// Two runs using the same seed, the same Config, and identical scenario
// execution ordering produce the same injected-fault patterns. Because OS
// scheduling can vary which goroutine draws which random value, exact replay
// is best-effort rather than guaranteed.
func SetSeed(seed int64) {
	// Use an atomic counter so that each new RNG from the pool gets a unique
	// PCG stream while still being derived deterministically from seed.
	var counter atomic.Uint64
	hi := uint64(seed)
	lo := uint64(seed) ^ 0x9e3779b97f4a7c15 // golden-ratio mix

	factory := func() any {
		n := counter.Add(1)
		// XOR with n and a distinct multiplier so hi ≠ lo even when seed = 0.
		return rand.New(rand.NewPCG(hi^n, lo^(n*0x517cc1b727220a95)))
	}
	activePool.Store(newPool(factory))
	seeded.Store(true)
}

// hasSeed reports whether SetSeed has been called.
func hasSeed() bool {
	return seeded.Load()
}

// randomFloat64 returns a uniformly distributed float64 in [0.0, 1.0) using
// a goroutine-local RNG from the active pool.
func randomFloat64() float64 {
	pool := activePool.Load()
	r := pool.Get().(*rand.Rand)
	v := r.Float64()
	pool.Put(r)
	return v
}

// randomInt64N returns a non-negative pseudo-random int64 in [0, n) using
// a goroutine-local RNG from the active pool.
func randomInt64N(n int64) int64 {
	pool := activePool.Load()
	r := pool.Get().(*rand.Rand)
	v := r.Int64N(n)
	pool.Put(r)
	return v
}
