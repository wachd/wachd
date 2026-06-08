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

import (
	"sync"
	"testing"
)

func TestState_FirstCallReturnsTrue(t *testing.T) {
	s := newState()
	if !s.IsNew("key", 1) {
		t.Fatal("first call with value > 0 should return true")
	}
}

func TestState_SameValueReturnsFalse(t *testing.T) {
	s := newState()
	s.IsNew("key", 3)
	if s.IsNew("key", 3) {
		t.Fatal("second call with same value should return false")
	}
}

func TestState_LowerValueReturnsFalse(t *testing.T) {
	s := newState()
	s.IsNew("key", 5)
	if s.IsNew("key", 3) {
		t.Fatal("lower value should return false (findings resolved)")
	}
}

func TestState_HigherValueReturnsTrue(t *testing.T) {
	s := newState()
	s.IsNew("key", 3)
	if !s.IsNew("key", 5) {
		t.Fatal("higher value should return true (new findings)")
	}
}

func TestState_ZeroValueReturnsFalse(t *testing.T) {
	s := newState()
	if s.IsNew("key", 0) {
		t.Fatal("zero value should return false — nothing to page about")
	}
}

func TestState_IndependentKeys(t *testing.T) {
	s := newState()
	s.IsNew("a", 10)
	if !s.IsNew("b", 1) {
		t.Fatal("different keys must be tracked independently")
	}
}

func TestState_StoresHighWaterMark(t *testing.T) {
	s := newState()
	s.IsNew("key", 5)
	s.IsNew("key", 3) // lower — updates nothing
	if !s.IsNew("key", 6) {
		t.Fatal("high-water mark must be 5, so 6 should still trigger")
	}
}

func TestState_ConcurrentAccess(t *testing.T) {
	s := newState()
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			s.IsNew("shared", n)
		}(i)
	}
	wg.Wait()
	// Verifying no data race — the race detector will catch any problem.
}
