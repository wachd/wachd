// Copyright 2025 NTC Dev
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package auth

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/store"
)

// ProviderCache is a 60-second in-memory cache of loaded OIDC providers.
// It avoids a DB round-trip on every SSO login while still picking up
// admin changes within one TTL window (or immediately via Invalidate).
type ProviderCache struct {
	mu          sync.Mutex
	db          *store.DB
	enc         *Encryptor
	redirectURI string
	ttl         time.Duration
	entries     map[uuid.UUID]*cacheEntry
}

type cacheEntry struct {
	provider  *OIDCProvider
	loadedAt  time.Time
}

// NewProviderCache creates a ProviderCache backed by the given DB and Encryptor.
// redirectURI is the OAuth2 callback URL (e.g. "https://example.com/auth/callback").
func NewProviderCache(db *store.DB, enc *Encryptor, redirectURI string, ttl time.Duration) *ProviderCache {
	return &ProviderCache{
		db:          db,
		enc:         enc,
		redirectURI: redirectURI,
		ttl:         ttl,
		entries:     make(map[uuid.UUID]*cacheEntry),
	}
}

// Get returns the OIDCProvider for the given DB provider ID,
// loading it from the database when the cache entry is missing or stale.
func (c *ProviderCache) Get(ctx context.Context, id uuid.UUID) (*OIDCProvider, error) {
	c.mu.Lock()
	entry, ok := c.entries[id]
	if ok && time.Since(entry.loadedAt) < c.ttl {
		c.mu.Unlock()
		return entry.provider, nil
	}
	c.mu.Unlock()

	// Load from DB (outside lock to avoid contention on slow OIDC discovery)
	record, err := c.db.GetSSOProvider(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("provider cache: get from db: %w", err)
	}
	if record == nil {
		return nil, fmt.Errorf("provider cache: provider %s not found", id)
	}
	if !record.Enabled {
		return nil, fmt.Errorf("provider cache: provider %s is disabled", id)
	}

	p, err := NewOIDCProviderFromRecord(ctx, record, c.enc, c.redirectURI)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.entries[id] = &cacheEntry{provider: p, loadedAt: time.Now()}
	c.mu.Unlock()

	return p, nil
}

// GetFirst returns the first enabled provider from the database.
// Used as a backward-compatible alias for the legacy /auth/login endpoint.
// Returns nil (no error) when no providers are configured.
func (c *ProviderCache) GetFirst(ctx context.Context) (*OIDCProvider, *uuid.UUID, string, error) {
	providers, err := c.db.ListSSOProviders(ctx, true)
	if err != nil {
		return nil, nil, "", fmt.Errorf("provider cache: list providers: %w", err)
	}
	if len(providers) == 0 {
		return nil, nil, "", nil
	}
	p, err := c.Get(ctx, providers[0].ID)
	if err != nil {
		return nil, nil, "", err
	}
	id := providers[0].ID
	return p, &id, providers[0].Name, nil
}

// Invalidate removes a provider from the cache, forcing a fresh load on next use.
// Call this after every admin create/update/delete.
func (c *ProviderCache) Invalidate(id uuid.UUID) {
	c.mu.Lock()
	delete(c.entries, id)
	c.mu.Unlock()
}
