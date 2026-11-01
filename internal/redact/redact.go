// Package redact hides secret values in text that is shown to the user.
package redact

import (
	"sort"
	"strings"
	"sync"
)

// Mask replaces secret values.
const Mask = "***"

// Set is a set of secret values.
type Set struct {
	mu     sync.RWMutex
	values []string
}

// Add registers a secret value. Empty and very short values are ignored to
// avoid masking unrelated text.
func (s *Set) Add(v string) {
	if s == nil || len(v) < 3 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.values {
		if e == v {
			return
		}
	}
	s.values = append(s.values, v)
	sort.Slice(s.values, func(i, j int) bool { return len(s.values[i]) > len(s.values[j]) })
}

// String replaces all secret values in text.
func (s *Set) String(text string) string {
	if s == nil {
		return text
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.values {
		text = strings.ReplaceAll(text, v, Mask)
	}
	return text
}

// Contains reports whether text contains a secret value.
func (s *Set) Contains(text string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.values {
		if strings.Contains(text, v) {
			return true
		}
	}
	return false
}

// Empty reports whether no secrets are registered.
func (s *Set) Empty() bool {
	if s == nil {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.values) == 0
}

// Values returns the secret values, longest first.
func (s *Set) Values() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.values...)
}
