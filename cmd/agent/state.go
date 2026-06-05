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

package main

import "sync"

// State tracks last-seen integer values per key to detect net-new findings.
// It is safe for concurrent use by multiple collector goroutines.
// State is in-memory only. On agent restart, collectors re-list existing objects
// and rebuild state before the watch stream begins, so events are not re-fired.
type State struct {
	mu      sync.Mutex
	entries map[string]int
}

func newState() *State {
	return &State{entries: make(map[string]int)}
}

// IsNew reports whether value has increased for key since the last call.
// If it has, the stored value is updated and true is returned.
// If the value is the same or lower (findings resolved), false is returned.
func (s *State) IsNew(key string, value int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value > s.entries[key] {
		s.entries[key] = value
		return true
	}
	return false
}
