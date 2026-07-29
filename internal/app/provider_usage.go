package app

import (
	"math"
	"strings"
	"time"

	"split/internal/diagnostics"
	"split/internal/state"
)

const providerUsageFallbackLifetime = 24 * time.Hour

// ApplyProviderUsage records a fresh provider window from an in-process
// collector and updates the footer without waiting for the SQLite refresh.
func (m *Model) ApplyProviderUsage(value state.ProviderUsage) bool {
	value.Provider = strings.ToLower(strings.TrimSpace(value.Provider))
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = time.Now()
	}
	if m.store != nil {
		if err := m.store.UpsertProviderUsage(value); err != nil {
			_ = diagnostics.Append(
				m.store.Path(),
				"usage",
				"cache_write_failed",
				diagnostics.Fields{"provider": value.Provider},
				err,
			)
			return false
		}
	}
	if m.providerUsage == nil {
		m.providerUsage = make(map[string]state.ProviderUsage, 2)
	}
	previous, existed := m.providerUsage[value.Provider]
	m.providerUsage[value.Provider] = value
	return !existed || !sameProviderUsageWindow(previous, value)
}

// RefreshProviderUsage picks up Claude status-line writes made by the managed
// helper process and removes expired cached windows from the visible model.
func (m *Model) RefreshProviderUsage(now time.Time) bool {
	if m.store == nil {
		return false
	}
	loaded, err := m.store.LoadProviderUsage()
	if err != nil {
		_ = diagnostics.Append(m.store.Path(), "usage", "cache_read_failed", nil, err)
		return false
	}
	next := make(map[string]state.ProviderUsage, len(loaded))
	for provider, value := range loaded {
		if providerUsageIsCurrent(value, now) {
			next[provider] = value
		}
	}
	changed := !sameVisibleProviderUsage(m.providerUsage, next)
	m.providerUsage = next
	return changed
}

func (m *Model) providerRemaining(
	provider string,
	now time.Time,
) (int, bool) {
	value, exists := m.providerUsage[provider]
	if !exists || !providerUsageIsCurrent(value, now) {
		return 0, false
	}
	remaining := int(math.Round(100 - value.UsedPercent))
	return max(0, min(100, remaining)), true
}

func providerUsageIsCurrent(value state.ProviderUsage, now time.Time) bool {
	if value.UpdatedAt.IsZero() {
		return false
	}
	if !value.ResetsAt.IsZero() {
		return now.Before(value.ResetsAt)
	}
	age := now.Sub(value.UpdatedAt)
	return age >= -time.Minute && age <= providerUsageFallbackLifetime
}

func sameVisibleProviderUsage(
	first map[string]state.ProviderUsage,
	second map[string]state.ProviderUsage,
) bool {
	if len(first) != len(second) {
		return false
	}
	for provider, value := range first {
		other, exists := second[provider]
		if !exists || !sameProviderUsageWindow(value, other) {
			return false
		}
	}
	return true
}

func sameProviderUsageWindow(first, second state.ProviderUsage) bool {
	return first.Provider == second.Provider &&
		math.Abs(first.UsedPercent-second.UsedPercent) < 0.0001 &&
		first.ResetsAt.Equal(second.ResetsAt)
}
