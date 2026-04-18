package state

import (
	"sync"
	"testing"
)

func TestSeqGenMonotonic(t *testing.T) {
	var g SeqGen
	var last uint64
	for i := 0; i < 1000; i++ {
		n := g.Next()
		if n <= last {
			t.Fatalf("seq not increasing: got %d after %d", n, last)
		}
		last = n
	}
}

func TestSeqGenConcurrentUnique(t *testing.T) {
	var g SeqGen
	const workers = 16
	const iters = 200
	var wg sync.WaitGroup
	seen := make(map[uint64]struct{})
	var mu sync.Mutex

	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				n := g.Next()
				mu.Lock()
				if _, dup := seen[n]; dup {
					mu.Unlock()
					t.Errorf("duplicate seq %d", n)
					return
				}
				seen[n] = struct{}{}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != workers*iters {
		t.Fatalf("unique seq count: got %d want %d", len(seen), workers*iters)
	}
}
