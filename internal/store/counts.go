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

// CountIncidentsThisMonth returns the number of incidents created in the current
// calendar month across all teams. Used by the license enforcer on webhook ingestion.
func (db *DB) CountIncidentsThisMonth(ctx context.Context) (int, error) {
	var n int
	err := db.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM incidents
		WHERE created_at >= date_trunc('month', NOW())
	`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count incidents this month: %w", err)
	}
	return n, nil
}
