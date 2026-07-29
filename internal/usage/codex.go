package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"split/internal/state"
)

const (
	codexPollInterval  = time.Minute
	codexRetryInterval = 30 * time.Second
	weeklyWindowMins   = int64(7 * 24 * 60)
)

type Event struct {
	Usage state.ProviderUsage
	Err   error
}

type Source interface {
	Events() <-chan Event
	Close() error
}

type CodexMonitor struct {
	events chan Event
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// StartCodexMonitor starts one hidden Codex app-server and reads the supported
// account/rateLimits endpoint over JSONL. It reuses the user's existing Codex
// login and never reads terminal output or conversation content.
func StartCodexMonitor() *CodexMonitor {
	ctx, cancel := context.WithCancel(context.Background())
	monitor := &CodexMonitor{
		events: make(chan Event, 4),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go monitor.run(ctx)
	return monitor
}

func (m *CodexMonitor) Events() <-chan Event {
	if m == nil {
		return nil
	}
	return m.events
}

func (m *CodexMonitor) Close() error {
	if m == nil {
		return nil
	}
	m.once.Do(m.cancel)
	<-m.done
	return nil
}

func (m *CodexMonitor) run(ctx context.Context) {
	defer close(m.done)
	defer close(m.events)
	for {
		err := runCodexAppServer(ctx, func(value state.ProviderUsage) {
			m.emit(Event{Usage: value})
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			m.emit(Event{Err: err})
		}
		timer := time.NewTimer(codexRetryInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func (m *CodexMonitor) emit(event Event) {
	select {
	case m.events <- event:
		return
	default:
	}
	select {
	case <-m.events:
	default:
	}
	select {
	case m.events <- event:
	default:
	}
}

type codexReadResult struct {
	message codexMessage
	err     error
}

type codexMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int64  `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func runCodexAppServer(
	ctx context.Context,
	publish func(state.ProviderUsage),
) error {
	executable, err := exec.LookPath("codex")
	if err != nil {
		return fmt.Errorf("locate Codex CLI: %w", err)
	}
	command := exec.CommandContext(ctx, executable, "app-server", "--listen", "stdio://")
	configureUsageCommand(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return fmt.Errorf("open Codex app-server input: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open Codex app-server output: %w", err)
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return fmt.Errorf("start Codex app-server: %w", err)
	}

	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	waited := false
	defer func() {
		_ = stdin.Close()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		if !waited {
			select {
			case <-exited:
			case <-time.After(time.Second):
			}
		}
	}()

	reads := make(chan codexReadResult, 16)
	go decodeCodexMessages(ctx, stdout, reads)
	encoder := json.NewEncoder(stdin)
	if err := encoder.Encode(map[string]any{
		"method": "initialize",
		"id":     int64(1),
		"params": map[string]any{
			"clientInfo": map[string]string{
				"name":    "split",
				"version": "0.1.0",
			},
		},
	}); err != nil {
		return fmt.Errorf("initialize Codex app-server: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case processErr := <-exited:
			waited = true
			return codexProcessError(processErr)
		case result := <-reads:
			if result.err != nil {
				return fmt.Errorf("read Codex app-server initialization: %w", result.err)
			}
			id, ok := codexMessageID(result.message.ID)
			if !ok || id != 1 {
				continue
			}
			if result.message.Error != nil {
				return fmt.Errorf(
					"Codex app-server initialization failed (%d): %s",
					result.message.Error.Code,
					result.message.Error.Message,
				)
			}
			if err := encoder.Encode(map[string]any{"method": "initialized"}); err != nil {
				return fmt.Errorf("finish Codex app-server initialization: %w", err)
			}
			return runCodexRateLimitLoop(ctx, encoder, reads, exited, &waited, publish)
		}
	}
}

func runCodexRateLimitLoop(
	ctx context.Context,
	encoder *json.Encoder,
	reads <-chan codexReadResult,
	exited <-chan error,
	waited *bool,
	publish func(state.ProviderUsage),
) error {
	nextID := int64(2)
	pendingID := int64(0)
	request := func() error {
		if pendingID != 0 {
			return nil
		}
		pendingID = nextID
		nextID++
		if err := encoder.Encode(map[string]any{
			"method": "account/rateLimits/read",
			"id":     pendingID,
			"params": nil,
		}); err != nil {
			return fmt.Errorf("request Codex rate limits: %w", err)
		}
		return nil
	}
	if err := request(); err != nil {
		return err
	}

	ticker := time.NewTicker(codexPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case processErr := <-exited:
			*waited = true
			return codexProcessError(processErr)
		case <-ticker.C:
			if err := request(); err != nil {
				return err
			}
		case result := <-reads:
			if result.err != nil {
				return fmt.Errorf("read Codex app-server rate limits: %w", result.err)
			}
			if result.message.Method == "account/rateLimits/updated" {
				if err := request(); err != nil {
					return err
				}
				continue
			}
			id, ok := codexMessageID(result.message.ID)
			if !ok || id != pendingID {
				continue
			}
			pendingID = 0
			if result.message.Error != nil {
				return fmt.Errorf(
					"Codex rate-limit request failed (%d): %s",
					result.message.Error.Code,
					result.message.Error.Message,
				)
			}
			value, available, err := parseCodexRateLimits(result.message.Result, time.Now())
			if err != nil {
				return err
			}
			if available {
				publish(value)
			}
		}
	}
}

func decodeCodexMessages(
	ctx context.Context,
	reader io.Reader,
	results chan<- codexReadResult,
) {
	decoder := json.NewDecoder(reader)
	for {
		var message codexMessage
		err := decoder.Decode(&message)
		select {
		case results <- codexReadResult{message: message, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

func codexMessageID(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var numeric int64
	if json.Unmarshal(raw, &numeric) == nil {
		return numeric, true
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		value, err := strconv.ParseInt(text, 10, 64)
		return value, err == nil
	}
	return 0, false
}

func codexProcessError(err error) error {
	if err == nil {
		return errors.New("Codex app-server exited")
	}
	return fmt.Errorf("Codex app-server exited: %w", err)
}

type codexRateLimitWindow struct {
	UsedPercent       *float64 `json:"usedPercent"`
	WindowDurationMin *int64   `json:"windowDurationMins"`
	ResetsAt          *int64   `json:"resetsAt"`
}

type codexRateLimitSnapshot struct {
	Primary   *codexRateLimitWindow `json:"primary"`
	Secondary *codexRateLimitWindow `json:"secondary"`
}

type codexRateLimitResponse struct {
	RateLimits          codexRateLimitSnapshot            `json:"rateLimits"`
	RateLimitsByLimitID map[string]codexRateLimitSnapshot `json:"rateLimitsByLimitId"`
}

func parseCodexRateLimits(
	result json.RawMessage,
	observedAt time.Time,
) (state.ProviderUsage, bool, error) {
	var response codexRateLimitResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return state.ProviderUsage{}, false, fmt.Errorf("decode Codex rate limits: %w", err)
	}
	window, ok := selectCodexWeeklyWindow(response)
	if !ok || window.UsedPercent == nil {
		return state.ProviderUsage{}, false, nil
	}
	usedPercent := *window.UsedPercent
	if math.IsNaN(usedPercent) || math.IsInf(usedPercent, 0) ||
		usedPercent < 0 || usedPercent > 100 {
		return state.ProviderUsage{}, false,
			fmt.Errorf("Codex weekly usage percentage is invalid: %v", usedPercent)
	}
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	value := state.ProviderUsage{
		Provider:    "codex",
		UsedPercent: usedPercent,
		UpdatedAt:   observedAt,
	}
	if window.ResetsAt != nil && *window.ResetsAt > 0 {
		value.ResetsAt = time.Unix(*window.ResetsAt, 0)
	}
	return value, true, nil
}

func selectCodexWeeklyWindow(
	response codexRateLimitResponse,
) (codexRateLimitWindow, bool) {
	snapshots := make([]codexRateLimitSnapshot, 0, len(response.RateLimitsByLimitID)+1)
	if codex, exists := response.RateLimitsByLimitID["codex"]; exists {
		snapshots = append(snapshots, codex)
	}
	keys := make([]string, 0, len(response.RateLimitsByLimitID))
	for key := range response.RateLimitsByLimitID {
		if strings.EqualFold(key, "codex") {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		snapshots = append(snapshots, response.RateLimitsByLimitID[key])
	}
	if response.RateLimits.Primary != nil || response.RateLimits.Secondary != nil {
		snapshots = append(snapshots, response.RateLimits)
	}

	windows := make([]codexRateLimitWindow, 0, len(snapshots)*2)
	for _, snapshot := range snapshots {
		if snapshot.Primary != nil {
			windows = append(windows, *snapshot.Primary)
		}
		if snapshot.Secondary != nil {
			windows = append(windows, *snapshot.Secondary)
		}
	}
	for _, window := range windows {
		if window.UsedPercent != nil && window.WindowDurationMin != nil &&
			*window.WindowDurationMin == weeklyWindowMins {
			return window, true
		}
	}

	bestIndex := -1
	bestDuration := int64(-1)
	for index, window := range windows {
		if window.UsedPercent == nil || window.WindowDurationMin == nil {
			continue
		}
		if *window.WindowDurationMin > bestDuration {
			bestIndex = index
			bestDuration = *window.WindowDurationMin
		}
	}
	if bestIndex >= 0 {
		return windows[bestIndex], true
	}
	for index := len(snapshots) - 1; index >= 0; index-- {
		if snapshots[index].Secondary != nil && snapshots[index].Secondary.UsedPercent != nil {
			return *snapshots[index].Secondary, true
		}
		if snapshots[index].Primary != nil && snapshots[index].Primary.UsedPercent != nil {
			return *snapshots[index].Primary, true
		}
	}
	return codexRateLimitWindow{}, false
}
