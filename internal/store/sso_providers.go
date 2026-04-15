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

package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreateSSOProvider inserts a new SSO provider row.
// The caller is responsible for encrypting the client secret before passing it in.
func (db *DB) CreateSSOProvider(ctx context.Context, input SSOProviderInput) (*SSOProvider, error) {
	query := `
		INSERT INTO sso_providers
			(name, provider_type, issuer_url, client_id, client_secret_enc, scopes, enabled, auto_provision)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, name, provider_type, issuer_url, client_id, client_secret_enc,
		          scopes, enabled, auto_provision, created_at, updated_at
	`
	row := db.pool.QueryRow(ctx, query,
		input.Name, input.ProviderType, input.IssuerURL,
		input.ClientID, input.ClientSecretEnc, input.Scopes,
		input.Enabled, input.AutoProvision,
	)
	return scanSSOProvider(row)
}

// GetSSOProvider returns a single provider by ID.
func (db *DB) GetSSOProvider(ctx context.Context, id uuid.UUID) (*SSOProvider, error) {
	query := `
		SELECT id, name, provider_type, issuer_url, client_id, client_secret_enc,
		       scopes, enabled, auto_provision, created_at, updated_at
		FROM sso_providers
		WHERE id = $1
	`
	row := db.pool.QueryRow(ctx, query, id)
	p, err := scanSSOProvider(row)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// ListSSOProviders returns all providers. When enabledOnly is true, only enabled providers are returned.
func (db *DB) ListSSOProviders(ctx context.Context, enabledOnly bool) ([]SSOProvider, error) {
	query := `
		SELECT id, name, provider_type, issuer_url, client_id, client_secret_enc,
		       scopes, enabled, auto_provision, created_at, updated_at
		FROM sso_providers
	`
	if enabledOnly {
		query += " WHERE enabled = true"
	}
	query += " ORDER BY name"

	rows, err := db.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list sso providers: %w", err)
	}
	defer rows.Close()

	var providers []SSOProvider
	for rows.Next() {
		p, err := scanSSOProvider(rows)
		if err != nil {
			return nil, fmt.Errorf("scan sso provider: %w", err)
		}
		providers = append(providers, *p)
	}
	return providers, rows.Err()
}

// UpdateSSOProvider applies partial updates to a provider. Only non-nil fields are written.
func (db *DB) UpdateSSOProvider(ctx context.Context, id uuid.UUID, u SSOProviderUpdate) (*SSOProvider, error) {
	// Build dynamic SET clause
	args := []any{id}
	set := ""
	add := func(col string, val any) {
		if set != "" {
			set += ", "
		}
		args = append(args, val)
		set += fmt.Sprintf("%s = $%d", col, len(args))
	}

	if u.Name != nil {
		add("name", *u.Name)
	}
	if u.IssuerURL != nil {
		add("issuer_url", *u.IssuerURL)
	}
	if u.ClientID != nil {
		add("client_id", *u.ClientID)
	}
	if u.ClientSecretEnc != nil {
		add("client_secret_enc", *u.ClientSecretEnc)
	}
	if u.Scopes != nil {
		add("scopes", u.Scopes)
	}
	if u.Enabled != nil {
		add("enabled", *u.Enabled)
	}
	if u.AutoProvision != nil {
		add("auto_provision", *u.AutoProvision)
	}

	if set == "" {
		return db.GetSSOProvider(ctx, id)
	}
	set += ", updated_at = NOW()"

	query := fmt.Sprintf(`
		UPDATE sso_providers SET %s WHERE id = $1
		RETURNING id, name, provider_type, issuer_url, client_id, client_secret_enc,
		          scopes, enabled, auto_provision, created_at, updated_at
	`, set)

	row := db.pool.QueryRow(ctx, query, args...)
	return scanSSOProvider(row)
}

// DeleteSSOProvider removes a provider by ID. Returns pgx.ErrNoRows if not found.
func (db *DB) DeleteSSOProvider(ctx context.Context, id uuid.UUID) error {
	ct, err := db.pool.Exec(ctx, `DELETE FROM sso_providers WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete sso provider: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// scanSSOProvider reads a single SSOProvider from any pgx row/rows scanner.
func scanSSOProvider(row interface {
	Scan(dest ...any) error
}) (*SSOProvider, error) {
	var p SSOProvider
	err := row.Scan(
		&p.ID, &p.Name, &p.ProviderType, &p.IssuerURL,
		&p.ClientID, &p.ClientSecretEnc,
		&p.Scopes, &p.Enabled, &p.AutoProvision,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}
