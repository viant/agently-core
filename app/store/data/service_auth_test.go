package data

import (
	"errors"
	"strconv"
	"sync"
	"testing"
)

// TestAuthCacheConcurrent exercises get/set under contention so `go test -race`
// can detect any lapse in the mutex protecting byConversationID.
func TestAuthCacheConcurrent(t *testing.T) {
	cache := newAuthCache()
	var wg sync.WaitGroup
	writers := 8
	readers := 16
	perGoroutine := 500

	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				id := strconv.Itoa((i*perGoroutine + j) % 64)
				if j%3 == 0 {
					cache.set(id, errors.New("denied"))
				} else {
					cache.set(id, nil)
				}
			}
		}(i)
	}
	wg.Add(readers)
	for i := 0; i < readers; i++ {
		go func(i int) {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				id := strconv.Itoa((i*perGoroutine + j) % 64)
				_, _ = cache.get(id)
			}
		}(i)
	}
	wg.Wait()
}

func TestAuthCacheNilSafe(t *testing.T) {
	var c *authCache
	if _, ok := c.get("x"); ok {
		t.Fatalf("nil cache should miss")
	}
	c.set("x", errors.New("ignored")) // must not panic
}
