package policy

import (
	"sync"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// cache stores global locks, seller-type locks, and seller overrides.
// Entries have a TTL (default 5s) used as a fallback if NOTIFY fails.
type cache struct {
	mu          sync.RWMutex
	globalLocks map[Key]timedValue
	typeLocks   map[string]map[Key]timedValue // seller_type → key → value
	overrides   map[core.SellerID]map[Key]timedValue
	ttl         time.Duration
	clock       core.Clock
}

type timedValue struct {
	value     Value
	expiresAt time.Time
}

func newCache(ttl time.Duration, clock core.Clock) *cache {
	return &cache{
		globalLocks: make(map[Key]timedValue),
		typeLocks:   make(map[string]map[Key]timedValue),
		overrides:   make(map[core.SellerID]map[Key]timedValue),
		ttl:         ttl,
		clock:       clock,
	}
}

func (c *cache) GlobalLock(key Key) (Value, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if tv, ok := c.globalLocks[key]; ok && c.clock.Now().Before(tv.expiresAt) {
		return tv.value, true
	}
	return Value{}, false
}

func (c *cache) SellerTypeLock(sellerType core.SellerType, key Key) (Value, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if m, ok := c.typeLocks[string(sellerType)]; ok {
		if tv, ok := m[key]; ok && c.clock.Now().Before(tv.expiresAt) {
			return tv.value, true
		}
	}
	return Value{}, false
}

func (c *cache) SellerOverride(sellerID core.SellerID, key Key) (Value, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if m, ok := c.overrides[sellerID]; ok {
		if tv, ok := m[key]; ok && c.clock.Now().Before(tv.expiresAt) {
			return tv.value, true
		}
	}
	return Value{}, false
}

func (c *cache) SetGlobalLock(key Key, value Value) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.globalLocks[key] = timedValue{value: value, expiresAt: c.clock.Now().Add(c.ttl)}
}

func (c *cache) SetSellerTypeLock(sellerType core.SellerType, key Key, value Value) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.typeLocks[string(sellerType)] == nil {
		c.typeLocks[string(sellerType)] = make(map[Key]timedValue)
	}
	c.typeLocks[string(sellerType)][key] = timedValue{value: value, expiresAt: c.clock.Now().Add(c.ttl)}
}

func (c *cache) SetSellerOverride(sellerID core.SellerID, key Key, value Value) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.overrides[sellerID] == nil {
		c.overrides[sellerID] = make(map[Key]timedValue)
	}
	c.overrides[sellerID][key] = timedValue{value: value, expiresAt: c.clock.Now().Add(c.ttl)}
}

func (c *cache) InvalidateSeller(sellerID core.SellerID, key Key) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok := c.overrides[sellerID]; ok {
		delete(m, key)
	}
}

func (c *cache) InvalidateGlobalLock(key Key) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.globalLocks, key)
}

func (c *cache) InvalidateTypeLock(sellerType core.SellerType, key Key) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok := c.typeLocks[string(sellerType)]; ok {
		delete(m, key)
	}
}

func (c *cache) BulkSetOverrides(sellerID core.SellerID, overrides map[Key]Value) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := make(map[Key]timedValue, len(overrides))
	exp := c.clock.Now().Add(c.ttl)
	for k, v := range overrides {
		m[k] = timedValue{value: v, expiresAt: exp}
	}
	c.overrides[sellerID] = m
}

func (c *cache) BulkSetLocks(globalLocks map[Key]Value, typeLocks map[string]map[Key]Value) {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp := c.clock.Now().Add(c.ttl)
	for k, v := range globalLocks {
		c.globalLocks[k] = timedValue{value: v, expiresAt: exp}
	}
	for t, keys := range typeLocks {
		if c.typeLocks[t] == nil {
			c.typeLocks[t] = make(map[Key]timedValue)
		}
		for k, v := range keys {
			c.typeLocks[t][k] = timedValue{value: v, expiresAt: exp}
		}
	}
}
