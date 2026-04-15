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

package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Queue wraps Redis operations for job queue
type Queue struct {
	client *redis.Client
}

// Job represents a job in the queue
type Job struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"` // "alert", "cve_scan", etc.
	IncidentID uuid.UUID `json:"incident_id,omitempty"`
	TeamID     uuid.UUID `json:"team_id"`
	Payload    []byte    `json:"payload"`
	CreatedAt  time.Time `json:"created_at"`
}

const (
	alertQueueKey = "wachd:queue:alerts"
)

// NewQueue creates a new queue client
func NewQueue(redisURL string) (*Queue, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Redis URL: %w", err)
	}

	client := redis.NewClient(opt)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	return &Queue{client: client}, nil
}

// Close closes the Redis connection
func (q *Queue) Close() error {
	return q.client.Close()
}

// EnqueueAlert enqueues an alert processing job
func (q *Queue) EnqueueAlert(ctx context.Context, incidentID, teamID uuid.UUID, payload []byte) error {
	job := Job{
		ID:         uuid.New().String(),
		Type:       "alert",
		IncidentID: incidentID,
		TeamID:     teamID,
		Payload:    payload,
		CreatedAt:  time.Now(),
	}

	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to marshal job: %w", err)
	}

	if err := q.client.RPush(ctx, alertQueueKey, data).Err(); err != nil {
		return fmt.Errorf("failed to enqueue job: %w", err)
	}

	return nil
}

// DequeueAlert dequeues an alert processing job (blocking)
func (q *Queue) DequeueAlert(ctx context.Context, timeout time.Duration) (*Job, error) {
	result, err := q.client.BLPop(ctx, timeout, alertQueueKey).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // No job available
		}
		return nil, fmt.Errorf("failed to dequeue job: %w", err)
	}

	if len(result) < 2 {
		return nil, fmt.Errorf("invalid result from BLPOP")
	}

	job := &Job{}
	if err := json.Unmarshal([]byte(result[1]), job); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job: %w", err)
	}

	return job, nil
}

// GetQueueLength returns the number of jobs in the alert queue
func (q *Queue) GetQueueLength(ctx context.Context) (int64, error) {
	return q.client.LLen(ctx, alertQueueKey).Result()
}
