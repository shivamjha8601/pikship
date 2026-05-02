package auth

import (
	"container/list"
	"sync"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

const (
	defaultCacheTTL = 30 * time.Second
	defaultCacheCap = 10_000
)

type cacheEntry struct {
	hash       string
	principal  Principal
	insertedAt time.Time
}

type sessionCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List
	cap     int
	ttl     time.Duration
	clock   core.Clock
}

func newSessionCache(clock core.Clock) *sessionCache {
	return &sessionCache{
		entries: make(map[string]*list.Element),
		order:   list.New(),
		cap:     defaultCacheCap,
		ttl:     defaultCacheTTL,
		clock:   clock,
	}
}

func (c *sessionCache) get(hash string) (Principal, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.entries[hash]
	if !ok {
		return Principal{}, false
	}
	entry := el.Value.(*cacheEntry)
	if c.clock.Since(entry.insertedAt) > c.ttl {
		c.order.Remove(el)
		delete(c.entries, hash)
		return Principal{}, false
	}
	c.order.MoveToBack(el)
	return entry.principal, true
}

func (c *sessionCache) set(hash string, p Principal) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.entries[hash]; ok {
		c.order.MoveToBack(el)
		el.Value.(*cacheEntry).principal = p
		el.Value.(*cacheEntry).insertedAt = c.clock.Now()
		return
	}
	if c.order.Len() >= c.cap {
		front := c.order.Front()
		if front != nil {
			oldest := front.Value.(*cacheEntry)
			delete(c.entries, oldest.hash)
			c.order.Remove(front)
		}
	}
	entry := &cacheEntry{hash: hash, principal: p, insertedAt: c.clock.Now()}
	el := c.order.PushBack(entry)
	c.entries[hash] = el
}

func (c *sessionCache) delete(hash string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[hash]; ok {
		c.order.Remove(el)
		delete(c.entries, hash)
	}
}
