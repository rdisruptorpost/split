package usage

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseCodexRateLimitsSelectsSevenDayWindow(t *testing.T) {
	observedAt := time.Unix(1_800_000_000, 0)
	resetAt := observedAt.Add(5 * 24 * time.Hour).Unix()
	payload := json.RawMessage(`{
		"rateLimits": {
			"primary": {"usedPercent": 7, "windowDurationMins": 300, "resetsAt": 1800000300},
			"secondary": {"usedPercent": 28, "windowDurationMins": 10080, "resetsAt": ` +
		jsonNumber(resetAt) + `}
		},
		"rateLimitsByLimitId": {}
	}`)
	value, available, err := parseCodexRateLimits(payload, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !available {
		t.Fatal("weekly Codex limit should be available")
	}
	if value.Provider != "codex" || value.UsedPercent != 28 ||
		!value.ResetsAt.Equal(time.Unix(resetAt, 0)) ||
		!value.UpdatedAt.Equal(observedAt) {
		t.Fatalf("unexpected Codex usage: %#v", value)
	}
}

func TestParseCodexRateLimitsPrefersNamedCodexWindow(t *testing.T) {
	payload := json.RawMessage(`{
		"rateLimits": {"secondary": {"usedPercent": 90, "windowDurationMins": 10080}},
		"rateLimitsByLimitId": {
			"codex": {"secondary": {"usedPercent": 35.5, "windowDurationMins": 10080}}
		}
	}`)
	value, available, err := parseCodexRateLimits(payload, time.Unix(1, 0))
	if err != nil || !available {
		t.Fatalf("parse Codex limit: available=%v err=%v", available, err)
	}
	if math.Abs(value.UsedPercent-35.5) > 0.0001 {
		t.Fatalf("used percent = %v, want 35.5", value.UsedPercent)
	}
}

func TestParseClaudeStatusLineHandlesMissingAndWeeklyLimits(t *testing.T) {
	if _, available, err := ParseClaudeStatusLine(
		strings.NewReader(`{"session_id":"before-first-response"}`),
		time.Unix(1, 0),
	); err != nil || available {
		t.Fatalf("missing rate limits: available=%v err=%v", available, err)
	}

	observedAt := time.Unix(1_800_000_000, 0)
	value, available, err := ParseClaudeStatusLine(strings.NewReader(`{
		"rate_limits": {
			"five_hour": {"used_percentage": 12.5, "resets_at": 1800000300},
			"seven_day": {"used_percentage": 56.2, "resets_at": 1800600000}
		}
	}`), observedAt)
	if err != nil || !available {
		t.Fatalf("parse Claude limit: available=%v err=%v", available, err)
	}
	if value.Provider != "claude" || math.Abs(value.UsedPercent-56.2) > 0.0001 ||
		!value.ResetsAt.Equal(time.Unix(1_800_600_000, 0)) {
		t.Fatalf("unexpected Claude usage: %#v", value)
	}
}

// This opt-in test is useful when Codex changes its app-server wire shape.
// It is skipped in normal CI because it reads the developer's live account.
func TestLiveCodexMonitor(t *testing.T) {
	if os.Getenv("SPLIT_LIVE_CODEX_USAGE") != "1" {
		t.Skip("set SPLIT_LIVE_CODEX_USAGE=1 to query the signed-in Codex account")
	}
	monitor := StartCodexMonitor()
	defer monitor.Close()
	select {
	case event := <-monitor.Events():
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Usage.Provider != "codex" {
			t.Fatalf("unexpected live event: %#v", event)
		}
		t.Logf("live Codex weekly usage: %#v", event.Usage)
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for Codex weekly usage")
	}
}

func jsonNumber(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
