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
)

// GetPasswordPolicy returns the singleton password policy row (always id=1).
func (db *DB) GetPasswordPolicy(ctx context.Context) (*PasswordPolicy, error) {
	query := `
		SELECT min_length, require_uppercase, require_lowercase,
		       require_number, require_special,
		       max_failed_attempts, lockout_duration_minutes, updated_at
		FROM password_policy
		WHERE id = 1
	`
	row := db.pool.QueryRow(ctx, query)

	var p PasswordPolicy
	err := row.Scan(
		&p.MinLength, &p.RequireUppercase, &p.RequireLowercase,
		&p.RequireNumber, &p.RequireSpecial,
		&p.MaxFailedAttempts, &p.LockoutDurationMinutes, &p.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get password policy: %w", err)
	}
	return &p, nil
}

// UpdatePasswordPolicy applies partial updates to the singleton password policy.
// Only non-nil fields are written. Returns the updated record.
func (db *DB) UpdatePasswordPolicy(ctx context.Context, u PasswordPolicyUpdate) (*PasswordPolicy, error) {
	args := []any{}
	set := ""
	add := func(col string, val any) {
		if set != "" {
			set += ", "
		}
		args = append(args, val)
		set += fmt.Sprintf("%s = $%d", col, len(args))
	}

	if u.MinLength != nil {
		add("min_length", *u.MinLength)
	}
	if u.RequireUppercase != nil {
		add("require_uppercase", *u.RequireUppercase)
	}
	if u.RequireLowercase != nil {
		add("require_lowercase", *u.RequireLowercase)
	}
	if u.RequireNumber != nil {
		add("require_number", *u.RequireNumber)
	}
	if u.RequireSpecial != nil {
		add("require_special", *u.RequireSpecial)
	}
	if u.MaxFailedAttempts != nil {
		add("max_failed_attempts", *u.MaxFailedAttempts)
	}
	if u.LockoutDurationMinutes != nil {
		add("lockout_duration_minutes", *u.LockoutDurationMinutes)
	}

	if set == "" {
		return db.GetPasswordPolicy(ctx)
	}
	set += ", updated_at = NOW()"

	query := fmt.Sprintf(`
		UPDATE password_policy SET %s WHERE id = 1
		RETURNING min_length, require_uppercase, require_lowercase,
		          require_number, require_special,
		          max_failed_attempts, lockout_duration_minutes, updated_at
	`, set)

	row := db.pool.QueryRow(ctx, query, args...)

	var p PasswordPolicy
	err := row.Scan(
		&p.MinLength, &p.RequireUppercase, &p.RequireLowercase,
		&p.RequireNumber, &p.RequireSpecial,
		&p.MaxFailedAttempts, &p.LockoutDurationMinutes, &p.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("update password policy: %w", err)
	}
	return &p, nil
}
