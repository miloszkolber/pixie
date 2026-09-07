package controller

import (
	"context"
	"fmt"
	"sync"
)

type replayResult struct {
	done  chan struct{}
	value []byte
	err   error
}

type replayEntry struct {
	fingerprint string
	result      *replayResult
	settled     bool
	weight      int
}

type replayNamespace struct {
	requests map[string]*replayEntry
	weight   int
}

type ReplayCache struct {
	mu          sync.Mutex
	clients     map[string]*replayNamespace
	maxRequests int
	maxWeight   int
}

func NewReplayCache() *ReplayCache {
	return &ReplayCache{clients: make(map[string]*replayNamespace), maxRequests: 512, maxWeight: 16 * 1024 * 1024}
}

// Run retains the bytes transferred by execute. Neither execute nor callers may
// modify or reuse that storage: first delivery and retries share the same result.
func (c *ReplayCache) Run(ctx context.Context, client, id, fingerprint string, execute func() ([]byte, error)) ([]byte, error) {
	c.mu.Lock()
	namespace := c.clients[client]
	if namespace == nil {
		namespace = &replayNamespace{requests: make(map[string]*replayEntry)}
		c.clients[client] = namespace
	}
	if existing := namespace.requests[id]; existing != nil {
		if existing.fingerprint != fingerprint {
			c.mu.Unlock()
			return nil, fmt.Errorf("request id %q was reused with a different payload", id)
		}
		if existing.result == nil {
			c.mu.Unlock()
			return nil, fmt.Errorf("request id %q already executed; its response exceeded the retention budget", id)
		}
		result := existing.result
		c.mu.Unlock()
		select {
		case <-result.done:
			return result.value, result.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if len(namespace.requests) >= c.maxRequests {
		c.mu.Unlock()
		return nil, fmt.Errorf("replay namespace for client %q is full: unacknowledged results must be read first", client)
	}
	result := &replayResult{done: make(chan struct{})}
	entry := &replayEntry{fingerprint: fingerprint, result: result}
	namespace.requests[id] = entry
	c.mu.Unlock()

	value, err := execute()
	c.mu.Lock()
	result.value, result.err = value, err
	entry.settled = true
	weight := 1
	if err == nil {
		weight = len(value)
	}
	if namespace.weight+weight > c.maxWeight {
		// Existing waiters own the in-flight result. Keep only its fingerprint in
		// the cache so later retries cannot repeat an already executed mutation.
		entry.result = nil
	} else {
		entry.weight = weight
		namespace.weight += weight
	}
	close(result.done)
	c.mu.Unlock()
	return value, err
}

func (c *ReplayCache) Acknowledge(client string, ids []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	namespace := c.clients[client]
	if namespace == nil {
		return
	}
	for _, id := range ids {
		entry := namespace.requests[id]
		if entry != nil && entry.settled {
			delete(namespace.requests, id)
			namespace.weight -= entry.weight
		}
	}
}

func (c *ReplayCache) Retain(client string, ids []string) {
	keep := make(map[string]bool, len(ids))
	for _, id := range ids {
		keep[id] = true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	namespace := c.clients[client]
	if namespace == nil {
		return
	}
	for id, entry := range namespace.requests {
		if !keep[id] && entry.settled {
			delete(namespace.requests, id)
			namespace.weight -= entry.weight
		}
	}
}

func (c *ReplayCache) ClearClient(client string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	namespace := c.clients[client]
	if namespace == nil {
		return true
	}
	for _, entry := range namespace.requests {
		if !entry.settled {
			return false
		}
	}
	delete(c.clients, client)
	return true
}
