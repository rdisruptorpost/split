package agent

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

const (
	startupGrace        = 3 * time.Second
	submissionHold      = 1500 * time.Millisecond
	outputActiveWindow  = 1250 * time.Millisecond
	interruptionDisplay = 2 * time.Second
)

// Kind is a coding agent recognized inside a terminal pane.
type Kind uint8

const (
	KindUnknown Kind = iota
	KindCodex
	KindClaude
)

func (kind Kind) Label() string {
	switch kind {
	case KindCodex:
		return "Codex"
	case KindClaude:
		return "Claude"
	default:
		return "Agent"
	}
}

// Status is the live state shown for an agent in the project sidebar.
type Status uint8

const (
	StatusLoading Status = iota
	StatusIdle
	StatusWorking
	StatusBlocked
	StatusFinished
	StatusInterrupted
	StatusExited
)

func (status Status) Label() string {
	switch status {
	case StatusLoading:
		return "loading"
	case StatusIdle:
		return "idle"
	case StatusWorking:
		return "working"
	case StatusBlocked:
		return "blocked"
	case StatusFinished:
		return "done"
	case StatusInterrupted:
		return "interrupted"
	case StatusExited:
		return "exited"
	default:
		return "unknown"
	}
}

// State is the latest agent observation for one pane.
type State struct {
	PaneID string
	PID    uint32
	Kind   Kind
	Status Status
	Since  time.Time
}

// Target is the terminal evidence used during one process scan.
type Target struct {
	PaneID     string
	RootPID    uint32
	Screen     string
	Title      string
	LastOutput time.Time
	TerminalUp bool
}

type trackedState struct {
	State
	startedAt          time.Time
	lastOutput         time.Time
	workingHoldUntil   time.Time
	interruptHoldUntil time.Time
	seenWorking        bool
}

type processInfo struct {
	pid       uint32
	parentPID uint32
	name      string
}

type snapshotFunc func() ([]processInfo, error)
type commandLineFunc func(uint32) string

// Tracker follows agent processes across scans and preserves transition
// context so a working-to-idle transition can be shown as completed.
type Tracker struct {
	states      map[string]trackedState
	snapshot    snapshotFunc
	commandLine commandLineFunc
}

func NewTracker() *Tracker {
	return &Tracker{
		states:      make(map[string]trackedState),
		snapshot:    snapshotProcesses,
		commandLine: processCommandLine,
	}
}

func newTracker(snapshot snapshotFunc, commandLine commandLineFunc) *Tracker {
	return &Tracker{
		states:      make(map[string]trackedState),
		snapshot:    snapshot,
		commandLine: commandLine,
	}
}

// Refresh scans all processes once and returns one agent state per recognized
// pane. An exited agent stays attached to its pane until another agent starts
// there or the pane itself is removed.
func (tracker *Tracker) Refresh(targets []Target, now time.Time) map[string]State {
	if tracker == nil {
		return nil
	}
	processes, err := tracker.snapshot()
	if err != nil {
		return tracker.snapshotStates()
	}

	children := make(map[uint32][]processInfo)
	for _, process := range processes {
		children[process.parentPID] = append(children[process.parentPID], process)
	}

	next := make(map[string]trackedState)
	for _, target := range targets {
		if target.PaneID == "" {
			continue
		}
		previous, hadPrevious := tracker.states[target.PaneID]
		if !target.TerminalUp || target.RootPID == 0 {
			if hadPrevious {
				next[target.PaneID] = transition(previous, StatusExited, now)
			}
			continue
		}

		process, kind, found := discoverAgent(target.RootPID, children, tracker.commandLine)
		if !found {
			if hadPrevious {
				next[target.PaneID] = transition(previous, StatusExited, now)
			}
			continue
		}

		current := previous
		if !hadPrevious || previous.PID != process.pid || previous.Kind != kind ||
			previous.Status == StatusExited {
			current = trackedState{
				State: State{
					PaneID: target.PaneID,
					PID:    process.pid,
					Kind:   kind,
					Status: StatusLoading,
					Since:  now,
				},
				startedAt:  now,
				lastOutput: target.LastOutput,
			}
		}

		if target.LastOutput.After(current.lastOutput) {
			current.lastOutput = target.LastOutput
		}
		outputActive := !target.LastOutput.IsZero() &&
			now.Sub(target.LastOutput) <= outputActiveWindow
		if current.Status == StatusInterrupted && now.Before(current.interruptHoldUntil) {
			next[target.PaneID] = current
			continue
		}

		wasInterrupted := current.Status == StatusInterrupted
		signal := detectScreen(kind, target.Screen, target.Title)
		nextStatus := current.Status
		switch signal {
		case StatusBlocked:
			nextStatus = StatusBlocked
		case StatusWorking:
			nextStatus = StatusWorking
			current.seenWorking = true
		case StatusIdle:
			switch {
			case current.Status == StatusWorking &&
				(now.Before(current.workingHoldUntil) || outputActive):
				nextStatus = StatusWorking
			case wasInterrupted:
				current.seenWorking = false
				nextStatus = StatusIdle
			case current.seenWorking ||
				current.Status == StatusWorking ||
				current.Status == StatusBlocked:
				nextStatus = StatusFinished
			default:
				nextStatus = StatusIdle
			}
		default:
			switch {
			case current.Status == StatusWorking &&
				(now.Before(current.workingHoldUntil) || outputActive):
				nextStatus = StatusWorking
			case wasInterrupted:
				current.seenWorking = false
				nextStatus = StatusIdle
			case now.Sub(current.startedAt) < startupGrace:
				nextStatus = StatusLoading
			case current.Status == StatusLoading && outputActive:
				nextStatus = StatusWorking
				current.seenWorking = true
			case current.Status == StatusLoading:
				nextStatus = StatusIdle
			}
		}
		current = transition(current, nextStatus, now)
		next[target.PaneID] = current
	}

	tracker.states = next
	return tracker.snapshotStates()
}

// MarkSubmitted records an Enter key sent to a recognized agent. This is the
// strongest provider-neutral evidence that a new turn is beginning.
func (tracker *Tracker) MarkSubmitted(paneID string, now time.Time) bool {
	current, exists := tracker.states[paneID]
	if !exists || current.Status == StatusExited {
		return false
	}
	current.seenWorking = true
	current.workingHoldUntil = now.Add(submissionHold)
	current.interruptHoldUntil = time.Time{}
	current = transition(current, StatusWorking, now)
	tracker.states[paneID] = current
	return true
}

// MarkInterrupted records Esc while an agent is working and holds a visible
// interruption marker briefly before returning the pane to idle detection.
func (tracker *Tracker) MarkInterrupted(paneID string, now time.Time) bool {
	current, exists := tracker.states[paneID]
	if !exists || (current.Status != StatusWorking && current.Status != StatusLoading) {
		return false
	}
	current.seenWorking = false
	current.workingHoldUntil = time.Time{}
	current.interruptHoldUntil = now.Add(interruptionDisplay)
	current = transition(current, StatusInterrupted, now)
	tracker.states[paneID] = current
	return true
}

func transition(current trackedState, status Status, now time.Time) trackedState {
	if current.Status != status {
		current.Status = status
		current.Since = now
	}
	return current
}

func (tracker *Tracker) snapshotStates() map[string]State {
	result := make(map[string]State, len(tracker.states))
	for paneID, current := range tracker.states {
		result[paneID] = current.State
	}
	return result
}

type processCandidate struct {
	process processInfo
	depth   int
	kind    Kind
}

func discoverAgent(
	rootPID uint32,
	children map[uint32][]processInfo,
	commandLine commandLineFunc,
) (processInfo, Kind, bool) {
	type queuedProcess struct {
		process processInfo
		depth   int
	}
	queue := make([]queuedProcess, 0)
	for _, child := range children[rootPID] {
		queue = append(queue, queuedProcess{process: child, depth: 1})
	}

	var direct []processCandidate
	var wrappers []queuedProcess
	visited := map[uint32]bool{rootPID: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if visited[current.process.pid] {
			continue
		}
		visited[current.process.pid] = true

		if kind := directProcessKind(current.process.name); kind != KindUnknown {
			direct = append(direct, processCandidate{
				process: current.process,
				depth:   current.depth,
				kind:    kind,
			})
		} else if isGenericRuntime(current.process.name) {
			wrappers = append(wrappers, current)
		}
		for _, child := range children[current.process.pid] {
			queue = append(queue, queuedProcess{process: child, depth: current.depth + 1})
		}
	}

	if candidate, ok := selectCandidate(direct); ok {
		return candidate.process, candidate.kind, true
	}

	var wrapped []processCandidate
	for _, current := range wrappers {
		kind := wrappedProcessKind(commandLine(current.process.pid))
		if kind == KindUnknown {
			continue
		}
		wrapped = append(wrapped, processCandidate{
			process: current.process,
			depth:   current.depth,
			kind:    kind,
		})
	}
	if candidate, ok := selectCandidate(wrapped); ok {
		return candidate.process, candidate.kind, true
	}
	return processInfo{}, KindUnknown, false
}

func selectCandidate(candidates []processCandidate) (processCandidate, bool) {
	if len(candidates) == 0 {
		return processCandidate{}, false
	}
	slices.SortFunc(candidates, func(left, right processCandidate) int {
		if left.depth != right.depth {
			return left.depth - right.depth
		}
		return int(left.process.pid) - int(right.process.pid)
	})
	if len(candidates) > 1 &&
		candidates[0].depth == candidates[1].depth &&
		candidates[0].kind != candidates[1].kind {
		return processCandidate{}, false
	}
	return candidates[0], true
}

func directProcessKind(name string) Kind {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(name)))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	switch base {
	case "codex":
		return KindCodex
	case "claude", "claude-code":
		return KindClaude
	default:
		return KindUnknown
	}
}

func isGenericRuntime(name string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(name)))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	switch base {
	case "node", "bun", "deno", "cmd", "powershell", "pwsh", "bash", "sh":
		return true
	default:
		return false
	}
}

var (
	codexCommandPattern  = regexp.MustCompile(`(?i)(?:^|[\\/"'\s])(?:@openai[\\/])?codex(?:[-_.][a-z0-9]+)*(?:\.exe|\.cmd|\.bat|\.js)?(?:$|[\\/"'\s])`)
	claudeCommandPattern = regexp.MustCompile(`(?i)(?:^|[\\/"'\s])(?:@anthropic-ai[\\/])?claude(?:-code)?(?:[-_.][a-z0-9]+)*(?:\.exe|\.cmd|\.bat|\.js)?(?:$|[\\/"'\s])`)
)

func wrappedProcessKind(commandLine string) Kind {
	switch {
	case codexCommandPattern.MatchString(commandLine):
		return KindCodex
	case claudeCommandPattern.MatchString(commandLine):
		return KindClaude
	default:
		return KindUnknown
	}
}

func detectScreen(kind Kind, screen, title string) Status {
	recent := recentNonEmptyLines(strings.ToLower(ansi.Strip(screen)), 12)
	lowerTitle := strings.ToLower(title)

	if blockedScreen(kind, recent, lowerTitle) {
		return StatusBlocked
	}
	if workingScreen(kind, recent, lowerTitle) {
		return StatusWorking
	}
	if idleScreen(kind, recent, lowerTitle) {
		return StatusIdle
	}
	return StatusLoading
}

func blockedScreen(kind Kind, recent, title string) bool {
	if strings.Contains(title, "action required") {
		return true
	}
	strong := []string{
		"press enter to confirm or esc to cancel",
		"enter to submit answer",
		"enter to submit all",
		"allow command?",
		"waiting for permission",
		"do you want to allow this connection?",
		"run a dynamic workflow?",
	}
	for _, marker := range strong {
		if strings.Contains(recent, marker) {
			return true
		}
	}
	if strings.Contains(recent, "do you want to proceed?") &&
		(strings.Contains(recent, "yes") || strings.Contains(recent, "esc to cancel")) {
		return true
	}
	if kind == KindClaude &&
		strings.Contains(recent, "enter to select") &&
		strings.Contains(recent, "esc to cancel") {
		return true
	}
	return strings.Contains(recent, "[y/n]") || strings.Contains(recent, "yes (y)")
}

func workingScreen(kind Kind, recent, title string) bool {
	if hasBrailleSpinner(title) {
		return true
	}
	if strings.Contains(recent, "esc to interrupt") &&
		(strings.Contains(recent, "working (") || hasBrailleSpinner(recent)) {
		return true
	}
	if kind == KindClaude &&
		strings.Contains(recent, "/btw") &&
		strings.Contains(recent, "esc to close") {
		return true
	}
	return false
}

func idleScreen(kind Kind, recent, title string) bool {
	if kind == KindClaude {
		if strings.HasPrefix(strings.TrimSpace(title), "✳") {
			return true
		}
		for _, line := range strings.Split(recent, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "❯") {
				return true
			}
		}
		return false
	}

	if kind == KindCodex && title != "" && !hasBrailleSpinner(title) {
		return true
	}
	for _, line := range strings.Split(recent, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "›") || strings.HasPrefix(trimmed, ">_") {
			return true
		}
	}
	return false
}

func recentNonEmptyLines(content string, limit int) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	recent := make([]string, 0, limit)
	for index := len(lines) - 1; index >= 0 && len(recent) < limit; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		recent = append(recent, line)
	}
	slices.Reverse(recent)
	return strings.Join(recent, "\n")
}

func hasBrailleSpinner(content string) bool {
	for _, character := range content {
		if character >= '\u2801' && character <= '\u28ff' {
			return true
		}
	}
	return false
}
