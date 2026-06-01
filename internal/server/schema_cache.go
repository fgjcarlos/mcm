package server

import (
	"context"
	"sync"

	"github.com/fgjcarlos/mcm/internal/storage"
)

// jsonSchemaCache holds JSON schema definitions in memory so the hot MQTT
// message path does not issue a SQLite query per message. It is invalidated
// whenever schemas are created, updated, or deleted via the HTTP API.
type jsonSchemaCache struct {
	mu      sync.RWMutex
	schemas []storage.JSONSchemaDefinition
	loaded  bool
}

// get returns the cached schema definitions, loading them from the store on
// first use or after invalidation. The returned slice is shared and must be
// treated as read-only by callers (the cache replaces the whole slice on
// reload, never mutates it in place).
func (c *jsonSchemaCache) get(ctx context.Context, store *storage.Store) ([]storage.JSONSchemaDefinition, error) {
	c.mu.RLock()
	if c.loaded {
		schemas := c.schemas
		c.mu.RUnlock()
		return schemas, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	// Another goroutine may have loaded while we waited for the write lock.
	if c.loaded {
		return c.schemas, nil
	}
	schemas, err := store.ListJSONSchemas(ctx)
	if err != nil {
		return nil, err
	}
	c.schemas = schemas
	c.loaded = true
	return c.schemas, nil
}

// invalidate clears the cache so the next get reloads from the store.
func (c *jsonSchemaCache) invalidate() {
	c.mu.Lock()
	c.schemas = nil
	c.loaded = false
	c.mu.Unlock()
}
