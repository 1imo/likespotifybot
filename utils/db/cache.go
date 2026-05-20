package db

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

type readQueryCache struct {
	mu   sync.RWMutex
	rows map[string]rowsCacheEntry
	row  map[string]rowCacheEntry
}

type rowsCacheEntry struct {
	until time.Time
	cols  []string
	data  [][]interface{}
}

type rowCacheEntry struct {
	until time.Time
	vals  []interface{}
	isErr bool
	err   error
}

func newReadQueryCache() *readQueryCache {
	return &readQueryCache{
		rows: make(map[string]rowsCacheEntry),
		row:  make(map[string]rowCacheEntry),
	}
}

func (c *readQueryCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.rows = make(map[string]rowsCacheEntry)
	c.row = make(map[string]rowCacheEntry)
	c.mu.Unlock()
}

func (c *readQueryCache) keyQuery(query string, args []any) string {
	h := sha256.New()
	h.Write([]byte(query))
	h.Write([]byte{0})
	for _, a := range args {
		fmt.Fprintf(h, "%T:%v|", a, a)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (c *readQueryCache) getRows(key string) (cols []string, data [][]interface{}, ok bool) {
	if c == nil {
		return nil, nil, false
	}
	c.mu.RLock()
	e, ok := c.rows[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.until) {
		return nil, nil, false
	}
	return e.cols, cloneRowsData(e.data), true
}

func (c *readQueryCache) setRows(key string, cols []string, data [][]interface{}, ttl time.Duration) {
	if c == nil || ttl <= 0 {
		return
	}
	c.mu.Lock()
	c.rows[key] = rowsCacheEntry{
		until: time.Now().Add(ttl),
		cols:  cols,
		data:  cloneRowsData(data),
	}
	c.mu.Unlock()
}

func (c *readQueryCache) getRow(key string) (vals []interface{}, err error, ok bool) {
	if c == nil {
		return nil, nil, false
	}
	c.mu.RLock()
	e, ok := c.row[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.until) {
		return nil, nil, false
	}
	if e.isErr {
		return nil, e.err, true
	}
	return cloneRowVals(e.vals), nil, true
}

func (c *readQueryCache) setRowOK(key string, vals []interface{}, ttl time.Duration) {
	if c == nil || ttl <= 0 {
		return
	}
	c.mu.Lock()
	c.row[key] = rowCacheEntry{until: time.Now().Add(ttl), vals: cloneRowVals(vals)}
	c.mu.Unlock()
}

func (c *readQueryCache) setRowErr(key string, err error, ttl time.Duration) {
	if c == nil || ttl <= 0 {
		return
	}
	c.mu.Lock()
	c.row[key] = rowCacheEntry{until: time.Now().Add(ttl), isErr: true, err: err}
	c.mu.Unlock()
}
