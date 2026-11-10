package server

import "testing"

func TestAudioCacheEvictsOldestOverBudget(t *testing.T) {
	c := newAudioCache(10)
	mk := func(n int) *cached { return &cached{data: make([]byte, n)} }
	c.put("a", mk(4))
	c.put("b", mk(4))
	if _, ok := c.get("a"); !ok { // touching a makes b the oldest
		t.Fatal("a missing")
	}
	c.put("c", mk(4)) // total 12 > 10: evict the least recently used (b)
	if _, ok := c.get("b"); ok {
		t.Error("b should have been evicted")
	}
	if _, ok := c.get("a"); !ok {
		t.Error("a was evicted though recently used")
	}
	if _, ok := c.get("c"); !ok {
		t.Error("c missing")
	}
	c.put("huge", mk(11)) // larger than the whole budget: not cached
	if _, ok := c.get("huge"); ok {
		t.Error("oversized item cached")
	}
	if c.size > 10 {
		t.Errorf("size %d over budget", c.size)
	}
}
