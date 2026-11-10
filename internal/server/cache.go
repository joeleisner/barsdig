package server

import (
	"container/list"
	"sync"
)

// cached is a rendered cue kept in memory so seeking (many Range requests)
// does not re-render it.
type cached struct {
	data        []byte
	contentType string
	stub        bool
}

type cacheItem struct {
	key string
	val *cached
}

// audioCache is a size-bounded LRU of rendered audio.
type audioCache struct {
	mu   sync.Mutex
	max  int64
	size int64
	ll   *list.List
	m    map[string]*list.Element
}

func newAudioCache(max int64) *audioCache {
	return &audioCache{max: max, ll: list.New(), m: map[string]*list.Element{}}
}

func (c *audioCache) get(key string) (*cached, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(e)
	return e.Value.(*cacheItem).val, true
}

func (c *audioCache) put(key string, v *cached) {
	n := int64(len(v.data))
	if n > c.max {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[key]; ok {
		c.size -= int64(len(e.Value.(*cacheItem).val.data))
		c.ll.Remove(e)
		delete(c.m, key)
	}
	c.m[key] = c.ll.PushFront(&cacheItem{key, v})
	c.size += n
	for c.size > c.max {
		last := c.ll.Back()
		it := last.Value.(*cacheItem)
		c.size -= int64(len(it.val.data))
		delete(c.m, it.key)
		c.ll.Remove(last)
	}
}
