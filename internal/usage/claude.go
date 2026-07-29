package usage

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"time"

	"split/internal/state"
)

const maximumStatusLinePayload = 4 << 20

// ParseClaudeStatusLine extracts Claude Code's official seven-day rate-limit
// window. rate_limits is intentionally optional: it is absent before the first
// response and for accounts that do not expose subscription usage.
func ParseClaudeStatusLine(
	reader io.Reader,
	observedAt time.Time,
) (state.ProviderUsage, bool, error) {
	var payload struct {
		RateLimits *struct {
			SevenDay *struct {
				UsedPercentage *float64 `json:"used_percentage"`
				ResetsAt       *int64   `json:"resets_at"`
			} `json:"seven_day"`
		} `json:"rate_limits"`
	}
	decoder := json.NewDecoder(io.LimitReader(reader, maximumStatusLinePayload))
	if err := decoder.Decode(&payload); err != nil {
		return state.ProviderUsage{}, false, fmt.Errorf("decode Claude status line: %w", err)
	}
	if payload.RateLimits == nil || payload.RateLimits.SevenDay == nil ||
		payload.RateLimits.SevenDay.UsedPercentage == nil {
		return state.ProviderUsage{}, false, nil
	}

	usedPercent := *payload.RateLimits.SevenDay.UsedPercentage
	if math.IsNaN(usedPercent) || math.IsInf(usedPercent, 0) ||
		usedPercent < 0 || usedPercent > 100 {
		return state.ProviderUsage{}, false,
			fmt.Errorf("Claude weekly usage percentage is invalid: %v", usedPercent)
	}
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	value := state.ProviderUsage{
		Provider:    "claude",
		UsedPercent: usedPercent,
		UpdatedAt:   observedAt,
	}
	if payload.RateLimits.SevenDay.ResetsAt != nil &&
		*payload.RateLimits.SevenDay.ResetsAt > 0 {
		value.ResetsAt = time.Unix(*payload.RateLimits.SevenDay.ResetsAt, 0)
	}
	return value, true, nil
}
