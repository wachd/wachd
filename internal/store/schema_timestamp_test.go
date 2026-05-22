// Copyright 2025 NTC Dev
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package store

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

type issue25TimestampColumn struct {
	table  string
	column string
}

var issue25TimestampColumns = []issue25TimestampColumn{
	{table: "teams", column: "created_at"},
	{table: "teams", column: "updated_at"},

	{table: "users", column: "created_at"},
	{table: "users", column: "updated_at"},

	{table: "incidents", column: "fired_at"},
	{table: "incidents", column: "acknowledged_at"},
	{table: "incidents", column: "resolved_at"},
	{table: "incidents", column: "snoozed_until"},
	{table: "incidents", column: "created_at"},
	{table: "incidents", column: "updated_at"},

	{table: "schedules", column: "created_at"},
	{table: "schedules", column: "updated_at"},

	{table: "sso_identities", column: "created_at"},
	{table: "sso_identities", column: "updated_at"},

	{table: "sessions", column: "expires_at"},
	{table: "sessions", column: "created_at"},

	{table: "password_policy", column: "updated_at"},

	{table: "local_users", column: "locked_until"},
	{table: "local_users", column: "last_login_at"},
	{table: "local_users", column: "created_at"},
	{table: "local_users", column: "updated_at"},

	{table: "local_groups", column: "created_at"},
	{table: "local_groups", column: "updated_at"},

	{table: "local_group_members", column: "created_at"},

	{table: "group_mappings", column: "created_at"},

	{table: "sso_providers", column: "created_at"},
	{table: "sso_providers", column: "updated_at"},

	{table: "api_tokens", column: "last_used_at"},
	{table: "api_tokens", column: "expires_at"},
	{table: "api_tokens", column: "created_at"},

	{table: "schedule_overrides", column: "start_at"},
	{table: "schedule_overrides", column: "end_at"},
	{table: "schedule_overrides", column: "created_at"},

	{table: "escalation_policies", column: "updated_at"},

	{table: "team_config", column: "created_at"},
	{table: "team_config", column: "updated_at"},

	{table: "system_config", column: "updated_at"},
}

func TestSchemaUsesTimestamptzForColumnTypes(t *testing.T) {
	sqlWithoutCommentsOrStrings := stripSQLCommentsAndSingleQuotedStrings(schema)

	bareTimestamp := regexp.MustCompile(`(?i)\btimestamp\b`)
	if loc := bareTimestamp.FindStringIndex(sqlWithoutCommentsOrStrings); loc != nil {
		start := loc[0] - 80
		if start < 0 {
			start = 0
		}

		end := loc[1] + 80
		if end > len(sqlWithoutCommentsOrStrings) {
			end = len(sqlWithoutCommentsOrStrings)
		}

		t.Fatalf("schema still contains bare TIMESTAMP column type near: %q", sqlWithoutCommentsOrStrings[start:end])
	}
}

func TestSchemaIncludesIssue25TimestampMigration(t *testing.T) {
	if !strings.Contains(schema, "issue25_legacy_timestamp_columns") {
		t.Fatal("expected issue #25 legacy timestamp migration block")
	}

	if !strings.Contains(schema, "c.data_type = 'timestamp without time zone'") {
		t.Fatal("expected migration to only target timestamp without time zone columns")
	}

	if !strings.Contains(schema, "TYPE TIMESTAMPTZ USING %I AT TIME ZONE ''UTC''") {
		t.Fatal("expected migration to reinterpret existing timestamp values as UTC")
	}

	for _, column := range issue25TimestampColumns {
		want := fmt.Sprintf("('%s', '%s')", column.table, column.column)
		if !strings.Contains(schema, want) {
			t.Errorf("issue #25 migration missing %s.%s", column.table, column.column)
		}
	}
}

func stripSQLCommentsAndSingleQuotedStrings(sql string) string {
	var withoutComments strings.Builder

	for _, line := range strings.Split(sql, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}

		withoutComments.WriteString(line)
		withoutComments.WriteByte('\n')
	}

	var out strings.Builder
	inString := false
	text := withoutComments.String()

	for i := 0; i < len(text); i++ {
		ch := text[i]

		if inString {
			if ch == '\'' {
				if i+1 < len(text) && text[i+1] == '\'' {
					out.WriteByte(' ')
					i++
					continue
				}

				inString = false
			}

			out.WriteByte(' ')
			continue
		}

		if ch == '\'' {
			inString = true
			out.WriteByte(' ')
			continue
		}

		out.WriteByte(ch)
	}

	return out.String()
}
