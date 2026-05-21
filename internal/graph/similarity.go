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

package graph

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

const maxSimilarityReasons = 3

type similaritySignal struct {
	name       string
	reason     string
	weight     float64
	source     string
	candidate  string
	exactMatch bool
}

// scoreNodeSimilarity compares two graph nodes and returns the candidate wrapped
// in SimilarNode with a normalized 0.0-1.0 score.
//
// The scorer is intentionally metadata-aware instead of doing a plain keyword
// search. It gives separate weights to the signals called out in issue #36:
// root cause, log patterns, affected service, metric anomalies, deploys/commits,
// previous resolution, and the incident title/label.
//
// This is also the seam where a future pgvector embedding score can be blended
// in without changing the public FindSimilar API.
func scoreNodeSimilarity(source, candidate *Node) *SimilarNode {
	if candidate == nil {
		return &SimilarNode{Score: 0, Reason: "no candidate node"}
	}
	if source == nil {
		return &SimilarNode{Node: candidate, Score: 0, Reason: "no source node"}
	}

	sourceProps := parseProperties(source.Properties)
	candidateProps := parseProperties(candidate.Properties)

	signals := []similaritySignal{
		{
			name:      "title",
			reason:    "similar alert title",
			weight:    0.20,
			source:    source.Label,
			candidate: candidate.Label,
		},
		{
			name:      "root cause",
			reason:    "similar root cause text",
			weight:    0.25,
			source:    propertyText(sourceProps, "root_cause", "rootCause", "cause", "probable_cause"),
			candidate: propertyText(candidateProps, "root_cause", "rootCause", "cause", "probable_cause"),
		},
		{
			name:      "log pattern",
			reason:    "similar log pattern",
			weight:    0.20,
			source:    propertyText(sourceProps, "log_pattern", "log_patterns", "logs", "error_logs", "errorLogs"),
			candidate: propertyText(candidateProps, "log_pattern", "log_patterns", "logs", "error_logs", "errorLogs"),
		},
		{
			name:       "affected service",
			reason:     "same affected service",
			weight:     0.15,
			source:     propertyText(sourceProps, "service", "affected_service", "affectedService", "service_name", "serviceName"),
			candidate:  propertyText(candidateProps, "service", "affected_service", "affectedService", "service_name", "serviceName"),
			exactMatch: true,
		},
		{
			name:      "metric anomaly",
			reason:    "similar metric anomaly",
			weight:    0.10,
			source:    propertyText(sourceProps, "metric_anomaly", "metric_anomalies", "metrics", "metrics_summary", "metricsSummary"),
			candidate: propertyText(candidateProps, "metric_anomaly", "metric_anomalies", "metrics", "metrics_summary", "metricsSummary"),
		},
		{
			name:      "deployment",
			reason:    "similar deployment or commit context",
			weight:    0.05,
			source:    propertyText(sourceProps, "deployment", "deploy", "commit", "commits", "recent_commits", "recentCommits", "sha"),
			candidate: propertyText(candidateProps, "deployment", "deploy", "commit", "commits", "recent_commits", "recentCommits", "sha"),
		},
		{
			name:      "resolution",
			reason:    "similar previous resolution",
			weight:    0.05,
			source:    propertyText(sourceProps, "resolution", "resolved_by", "resolvedBy", "fix", "suggested_action", "suggestedAction"),
			candidate: propertyText(candidateProps, "resolution", "resolved_by", "resolvedBy", "fix", "suggested_action", "suggestedAction"),
		},
	}

	var weightedScore float64
	var availableWeight float64
	reasons := make([]string, 0, maxSimilarityReasons)

	for _, signal := range signals {
		if strings.TrimSpace(signal.source) == "" {
			continue
		}

		availableWeight += signal.weight

		score := fieldSimilarity(signal.source, signal.candidate, signal.exactMatch)
		weightedScore += signal.weight * score

		if score >= 0.65 && len(reasons) < maxSimilarityReasons {
			reason := signal.reason
			if signal.exactMatch {
				reason = fmt.Sprintf("%s: %s", signal.reason, shortText(signal.candidate, 80))
			}
			reasons = append(reasons, reason)
		}
	}

	if availableWeight == 0 {
		return &SimilarNode{
			Node:   candidate,
			Score:  0,
			Reason: "no comparable incident fields",
		}
	}

	score := clamp(weightedScore/availableWeight, 0, 1)
	reason := strings.Join(reasons, "; ")
	if reason == "" {
		if score > 0 {
			reason = "similar incident context"
		} else {
			reason = "no strong similarity signals"
		}
	}

	return &SimilarNode{
		Node:   candidate,
		Score:  roundScore(score),
		Reason: reason,
	}
}

func parseProperties(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return map[string]any{}
	}

	var props map[string]any
	if err := json.Unmarshal(raw, &props); err != nil {
		return map[string]any{}
	}

	return props
}

func propertyText(props map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := findProperty(props, normalizeKey(key))
		if !ok {
			continue
		}

		text := strings.TrimSpace(flattenValue(value))
		if text != "" {
			return text
		}
	}

	return ""
}

func findProperty(value any, normalizedKey string) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if normalizeKey(key) == normalizedKey {
				return child, true
			}
		}

		for _, child := range typed {
			if found, ok := findProperty(child, normalizedKey); ok {
				return found, true
			}
		}

	case []any:
		for _, child := range typed {
			if found, ok := findProperty(child, normalizedKey); ok {
				return found, true
			}
		}
	}

	return nil, false
}

func flattenValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64, float32, int, int64, int32, bool:
		return fmt.Sprint(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(flattenValue(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	case map[string]any:
		preferredKeys := []string{
			"message",
			"name",
			"label",
			"summary",
			"value",
			"metric",
			"service",
			"root_cause",
			"rootCause",
			"resolution",
			"sha",
		}

		parts := make([]string, 0, len(typed))
		for _, key := range preferredKeys {
			if value, ok := typed[key]; ok {
				if text := strings.TrimSpace(flattenValue(value)); text != "" {
					parts = append(parts, text)
				}
			}
		}

		if len(parts) > 0 {
			return strings.Join(parts, " ")
		}

		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		for _, key := range keys {
			if text := strings.TrimSpace(flattenValue(typed[key])); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	default:
		return fmt.Sprint(typed)
	}
}

func fieldSimilarity(source, candidate string, exactMatch bool) float64 {
	source = strings.TrimSpace(source)
	candidate = strings.TrimSpace(candidate)

	if source == "" || candidate == "" {
		return 0
	}

	if exactMatch {
		sourceNorm := normalizePhrase(source)
		candidateNorm := normalizePhrase(candidate)

		if sourceNorm == candidateNorm {
			return 1
		}

		if sourceNorm != "" && candidateNorm != "" &&
			(strings.Contains(sourceNorm, candidateNorm) || strings.Contains(candidateNorm, sourceNorm)) {
			return 0.75
		}
	}

	return tokenSimilarity(source, candidate)
}

func tokenSimilarity(left, right string) float64 {
	leftTokens := tokenSet(left)
	rightTokens := tokenSet(right)

	if len(leftTokens) == 0 || len(rightTokens) == 0 {
		return 0
	}

	intersection := 0
	for token := range leftTokens {
		if _, ok := rightTokens[token]; ok {
			intersection++
		}
	}

	if intersection == 0 {
		return 0
	}

	return float64(2*intersection) / float64(len(leftTokens)+len(rightTokens))
}

func tokenSet(text string) map[string]struct{} {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	tokens := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		token := strings.TrimSpace(field)
		if token == "" {
			continue
		}

		if _, stop := stopWords[token]; stop {
			continue
		}

		tokens[token] = struct{}{}
	}

	return tokens
}

func normalizePhrase(text string) string {
	tokens := tokenSet(text)
	if len(tokens) == 0 {
		return ""
	}

	parts := make([]string, 0, len(tokens))
	for token := range tokens {
		parts = append(parts, token)
	}
	sort.Strings(parts)

	return strings.Join(parts, " ")
}

func normalizeKey(key string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(key) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func shortText(text string, maxLen int) string {
	text = strings.TrimSpace(text)
	if len(text) <= maxLen {
		return text
	}

	if maxLen <= 1 {
		return text[:maxLen]
	}

	return text[:maxLen-1] + "…"
}

func clamp(value, minValue, maxValue float64) float64 {
	return math.Max(minValue, math.Min(maxValue, value))
}

func roundScore(value float64) float64 {
	return math.Round(value*1000) / 1000
}

var stopWords = map[string]struct{}{
	"a":       {},
	"an":      {},
	"and":     {},
	"are":     {},
	"as":      {},
	"at":      {},
	"be":      {},
	"by":      {},
	"for":     {},
	"from":    {},
	"has":     {},
	"have":    {},
	"in":      {},
	"is":      {},
	"it":      {},
	"of":      {},
	"on":      {},
	"or":      {},
	"that":    {},
	"the":     {},
	"this":    {},
	"to":      {},
	"was":     {},
	"were":    {},
	"with":    {},
	"after":   {},
	"before":  {},
	"during":  {},
	"service": {},
	"alert":   {},
	"error":   {},
	"errors":  {},
	"failure": {},
	"failed":  {},
}
