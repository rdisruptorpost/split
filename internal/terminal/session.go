package terminal

import (
	"errors"
	"image/color"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/aymanbagabas/go-pty"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

type State uint8

const (
	Starting State = iota
	Running
	Exited
	Failed
)

type EventKind uint8

const (
	Updated EventKind = iota
	ProcessExited
	ProcessFailed
)

type Event struct {
	SessionID string
	Kind      EventKind
	Err       error
}

type Command struct {
	Name string
	Args []string
	Dir  string
	Env  map[string]string
}

type CursorStyle = vt.CursorStyle

const (
	CursorBlock     = vt.CursorBlock
	CursorUnderline = vt.CursorUnderline
	CursorBar       = vt.CursorBar
)

type Cursor struct {
	X       int
	Y       int
	Visible bool
	Blink   bool
	Style   CursorStyle
	Color   color.Color
}

// ConPTY may split one full-screen repaint across several reads. Waiting for a
// short quiet period before moving the native Bubble Tea cursor prevents the
// renderer from exposing a child TUI's temporary drawing coordinates.
const cursorSettleDelay = 30 * time.Millisecond

type Session struct {
	id      string
	command Command
	events  chan<- Event

	mu               sync.RWMutex
	emulator         *vt.Emulator
	pseudoterm       pty.Pty
	process          *pty.Cmd
	state            State
	stateErr         error
	lastActivity     time.Time
	title            string
	cursorShown      bool
	cursorBlink      bool
	cursorStyle      vt.CursorStyle
	stableCursorX    int
	stableCursorY    int
	stableCursorSet  bool
	cursorPending    bool
	cursorDeadline   time.Time
	cursorTimer      *time.Timer
	scrollOffset     int
	selection        terminalSelection
	mouseModes       map[int]struct{}
	alternateScroll  bool
	outputFilter     outputFilter
	workingDirectory string
	promptReady      chan struct{}
	closed           chan struct{}
	promptReadyOnce  sync.Once

	closeOnce sync.Once
	workers   sync.WaitGroup
}

func Start(id string, command Command, width, height int, events chan<- Event) (*Session, error) {
	emulator := vt.NewEmulator(max(2, width), max(1, height))
	emulator.SetScrollbackSize(5_000)
	emulator.SetDefaultBackgroundColor(colorBackground)
	emulator.SetBackgroundColor(colorBackground)
	emulator.SetDefaultForegroundColor(colorForeground)
	emulator.SetForegroundColor(colorForeground)
	emulator.SetDefaultCursorColor(colorForeground)

	pseudoterm, err := pty.New()
	if err != nil {
		_ = emulator.Close()
		return nil, err
	}
	if err := pseudoterm.Resize(max(2, width), max(1, height)); err != nil {
		_ = pseudoterm.Close()
		_ = emulator.Close()
		return nil, err
	}

	cmd := pseudoterm.Command(command.Name, command.Args...)
	cmd.Dir = command.Dir
	cmd.Env = terminalEnvironment(os.Environ(), command.Env)

	initialCursor := emulator.CursorPosition()
	session := &Session{
		id:               id,
		command:          command,
		events:           events,
		emulator:         emulator,
		pseudoterm:       pseudoterm,
		process:          cmd,
		state:            Starting,
		lastActivity:     time.Now(),
		cursorShown:      true,
		cursorBlink:      true,
		cursorStyle:      vt.CursorBlock,
		stableCursorX:    initialCursor.X,
		stableCursorY:    initialCursor.Y,
		stableCursorSet:  true,
		mouseModes:       make(map[int]struct{}),
		workingDirectory: filepath.Clean(command.Dir),
		promptReady:      make(chan struct{}),
		closed:           make(chan struct{}),
	}
	emulator.SetCallbacks(vt.Callbacks{
		Title: func(title string) {
			session.title = title
		},
		WorkingDirectory: func(value string) {
			workingDirectory := normalizeWorkingDirectory(value)
			if workingDirectory == "" {
				return
			}
			session.workingDirectory = workingDirectory
			session.promptReadyOnce.Do(func() {
				close(session.promptReady)
			})
		},
		AltScreen: func(bool) {
			session.scrollOffset = 0
			session.selection = terminalSelection{}
		},
		EnableMode: func(mode ansi.Mode) {
			session.setTerminalMode(mode, true)
		},
		DisableMode: func(mode ansi.Mode) {
			session.setTerminalMode(mode, false)
		},
		CursorVisibility: func(visible bool) {
			session.cursorShown = visible
		},
		CursorStyle: func(style vt.CursorStyle, blink bool) {
			session.cursorStyle = style
			session.cursorBlink = blink
		},
	})

	if err := cmd.Start(); err != nil {
		_ = pseudoterm.Close()
		_ = emulator.Close()
		return nil, err
	}

	session.state = Running
	session.workers.Add(3)
	go func() {
		defer session.workers.Done()
		session.readOutput()
	}()
	go func() {
		defer session.workers.Done()
		session.forwardInput()
	}()
	go func() {
		defer session.workers.Done()
		session.waitForExit()
	}()
	return session, nil
}

func (s *Session) setTerminalMode(mode ansi.Mode, enabled bool) {
	if mode == nil {
		return
	}
	value := mode.Mode()
	if isMouseTrackingMode(value) {
		if enabled {
			s.mouseModes[value] = struct{}{}
		} else {
			delete(s.mouseModes, value)
		}
	}
	if value == 1007 {
		s.alternateScroll = enabled
	}
}

func isMouseTrackingMode(mode int) bool {
	switch mode {
	case 9, 1000, 1001, 1002, 1003:
		return true
	default:
		return false
	}
}

func ResolveCommand(name, dir string) (Command, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return Command{}, err
	}

	if runtime.GOOS == "windows" {
		extension := strings.ToLower(filepath.Ext(path))
		if extension == ".cmd" || extension == ".bat" {
			commandShell := os.Getenv("COMSPEC")
			if commandShell == "" {
				commandShell = "cmd.exe"
			}
			return Command{
				Name: commandShell,
				Args: []string{"/d", "/s", "/c", path},
				Dir:  dir,
			}, nil
		}
	}
	return Command{Name: path, Dir: dir}, nil
}

const powershellPromptIntegration = `$global:__splitOriginalPrompt = $function:prompt; function global:prompt { $splitPrompt = @(& $global:__splitOriginalPrompt) -join ' '; $splitLocation = $ExecutionContext.SessionState.Path.CurrentLocation; if ($splitLocation.Provider.Name -eq 'FileSystem') { $splitEscape = [string][char]27; $splitPrompt += $splitEscape + ']7;' + $splitLocation.ProviderPath + $splitEscape + '\' }; $splitPrompt }`

func DefaultShell(dir string) Command {
	if runtime.GOOS == "windows" {
		args := []string{"-NoLogo", "-NoExit", "-Command", powershellPromptIntegration}
		if path, err := exec.LookPath("pwsh.exe"); err == nil {
			return Command{Name: path, Args: args, Dir: dir}
		}
		if path, err := exec.LookPath("powershell.exe"); err == nil {
			return Command{Name: path, Args: args, Dir: dir}
		}
		if commandShell := os.Getenv("COMSPEC"); commandShell != "" {
			return Command{Name: commandShell, Dir: dir}
		}
		return Command{Name: "cmd.exe", Dir: dir}
	}

	if shell := os.Getenv("SHELL"); shell != "" {
		return Command{Name: shell, Dir: dir}
	}
	return Command{Name: "/bin/sh", Dir: dir}
}

func (s *Session) ID() string {
	return s.id
}

func (s *Session) State() (State, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state, s.stateErr
}

func (s *Session) LastActivity() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastActivity
}

func (s *Session) ProcessID() uint32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.process == nil || s.process.Process == nil {
		return 0
	}
	return uint32(s.process.Process.Pid)
}

func (s *Session) Title() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.title
}

// WorkingDirectory returns the most recent filesystem location emitted by the
// shell prompt. PowerShell does not update its Win32 process cwd after cd, so
// OSC 7 prompt integration is the authoritative source.
func (s *Session) WorkingDirectory() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workingDirectory
}

// SendCommandWhenReady waits for the first shell prompt before typing a
// provider resume command. The timeout keeps non-PowerShell fallback shells
// usable even when they do not emit OSC 7.
func (s *Session) SendCommandWhenReady(command string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return
	}
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		timer := time.NewTimer(8 * time.Second)
		defer timer.Stop()
		select {
		case <-s.promptReady:
		case <-timer.C:
		case <-s.closed:
			return
		}

		s.mu.Lock()
		defer s.mu.Unlock()
		if s.state != Running {
			return
		}
		s.scrollOffset = 0
		s.selection = terminalSelection{}
		s.emulator.SendText(command)
		s.emulator.SendKey(uv.KeyPressEvent(uv.Key{Code: vt.KeyEnter}))
	}()
}

func normalizeWorkingDirectory(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(value), "file://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return ""
		}
		path, err := url.PathUnescape(parsed.Path)
		if err != nil {
			return ""
		}
		if parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost") {
			value = `\\` + parsed.Host + filepath.FromSlash(path)
		} else {
			if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
				path = path[1:]
			}
			value = filepath.FromSlash(path)
		}
	}
	if !filepath.IsAbs(value) {
		return ""
	}
	return filepath.Clean(value)
}

func (s *Session) LiveRender() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.emulator.Render()
}

func (s *Session) Render() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.scrollOffset <= 0 && !s.selection.visible {
		return s.emulator.Render()
	}
	return renderTerminalViewport(s.emulator, s.scrollOffset, &s.selection)
}
func (s *Session) ScrollOffset() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.scrollOffset
}

func (s *Session) Cursor() Cursor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	position := s.emulator.CursorPosition()
	x, y := position.X, position.Y
	if s.cursorPending && s.stableCursorSet {
		x, y = s.stableCursorX, s.stableCursorY
	}
	return Cursor{
		X:       x,
		Y:       y,
		Visible: s.cursorShown && s.scrollOffset == 0 && !s.selection.visible,
		Blink:   s.cursorBlink,
		Style:   s.cursorStyle,
		Color:   s.emulator.CursorColor(),
	}
}

func (s *Session) Resize(width, height int) error {
	width = max(2, width)
	height = max(1, height)

	s.mu.Lock()
	s.selection = terminalSelection{}
	s.cancelCursorSettleLocked()
	previousScrollback := s.emulator.ScrollbackLen()
	s.emulator.Resize(width, height)
	s.snapshotCursorLocked()
	currentScrollback := s.emulator.ScrollbackLen()
	if s.scrollOffset > 0 && currentScrollback > previousScrollback {
		s.scrollOffset += currentScrollback - previousScrollback
	}
	s.scrollOffset = min(s.scrollOffset, currentScrollback)
	s.mu.Unlock()
	return s.pseudoterm.Resize(width, height)
}

func (s *Session) SendKey(message tea.KeyPressMsg) {
	key := tea.Key(message)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scrollOffset = 0
	s.selection = terminalSelection{}
	if text, ok := printableKeyText(key); ok {
		s.emulator.SendText(text)
		return
	}
	key.Mod &^= tea.ModCapsLock | tea.ModNumLock | tea.ModScrollLock
	s.emulator.SendKey(uv.KeyPressEvent(key))
}

func printableKeyText(key tea.Key) (string, bool) {
	if key.Text != "" {
		ctrl := key.Mod&tea.ModCtrl != 0
		alt := key.Mod&tea.ModAlt != 0
		other := key.Mod & (tea.ModMeta | tea.ModHyper | tea.ModSuper)
		if other == 0 && (!ctrl || alt) {
			if alt && !ctrl {
				return "\x1b" + key.Text, true
			}
			return key.Text, true
		}
	}

	commandModifiers := key.Mod & (tea.ModCtrl | tea.ModAlt | tea.ModMeta | tea.ModHyper | tea.ModSuper)
	if commandModifiers != 0 {
		return "", false
	}
	code := key.Code
	if key.Mod&tea.ModShift != 0 {
		switch {
		case key.ShiftedCode != 0:
			code = key.ShiftedCode
		case unicode.IsLetter(code):
			code = unicode.ToUpper(code)
		}
	}
	if unicode.IsPrint(code) {
		return string(code), true
	}
	return "", false
}

func (s *Session) Paste(content string) {
	s.mu.Lock()
	s.scrollOffset = 0
	s.selection = terminalSelection{}
	s.emulator.Paste(content)
	s.mu.Unlock()
}

func (s *Session) HandleWheel(x, y int, button tea.MouseButton, mod tea.KeyMod, lines int) bool {
	if lines == 0 || (button != tea.MouseWheelUp && button != tea.MouseWheelDown) {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.selection = terminalSelection{}
	x = max(0, min(x, s.emulator.Width()-1))
	y = max(0, min(y, s.emulator.Height()-1))

	if len(s.mouseModes) > 0 {
		s.scrollOffset = 0
		mouse := uv.Mouse{X: x, Y: y, Button: button, Mod: mod}
		s.emulator.SendMouse(uv.MouseWheelEvent(mouse))
		return true
	}
	if s.emulator.IsAltScreen() {
		if !s.alternateScroll {
			return false
		}
		keyCode := vt.KeyUp
		if lines < 0 {
			keyCode = vt.KeyDown
			lines = -lines
		}
		for range lines {
			s.emulator.SendKey(uv.KeyPressEvent(uv.Key{Code: keyCode}))
		}
		return true
	}
	return s.scrollLocked(lines)
}

func (s *Session) scrollLocked(lines int) bool {
	previous := s.scrollOffset
	s.scrollOffset = max(0, min(s.scrollOffset+lines, s.emulator.ScrollbackLen()))
	return s.scrollOffset != previous
}

func (s *Session) Close() {
	s.closeOnce.Do(func() {
		close(s.closed)
		s.mu.Lock()
		s.cancelCursorSettleLocked()
		process := s.process.Process
		if process != nil {
			_ = process.Kill()
		}
		if input, ok := s.emulator.InputPipe().(io.Closer); ok {
			_ = input.Close()
		}
		s.mu.Unlock()

		_ = s.pseudoterm.Close()
		s.workers.Wait()

		s.mu.Lock()
		_ = s.emulator.Close()
		s.mu.Unlock()
	})
}

func (s *Session) readOutput() {
	buffer := make([]byte, 32*1024)
	for {
		n, err := s.pseudoterm.Read(buffer)
		if n > 0 {
			filtered := s.outputFilter.Filter(buffer[:n])
			s.mu.Lock()
			var writeErr error
			if len(filtered) > 0 {
				s.ensureStableCursorLocked()
				previousScrollback := s.emulator.ScrollbackLen()
				_, writeErr = s.emulator.Write(filtered)
				currentScrollback := s.emulator.ScrollbackLen()
				if s.scrollOffset > 0 && !s.emulator.IsAltScreen() {
					if added := currentScrollback - previousScrollback; added > 0 {
						s.scrollOffset += added
					}
					s.scrollOffset = min(s.scrollOffset, currentScrollback)
				}
				if writeErr == nil {
					s.scheduleCursorSettleLocked()
				}
			}
			s.lastActivity = time.Now()
			s.mu.Unlock()
			if writeErr != nil {
				s.fail(writeErr)
				return
			}
			s.notify(Event{SessionID: s.id, Kind: Updated})
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !isClosedError(err) {
				s.fail(err)
			}
			return
		}
	}
}

func (s *Session) ensureStableCursorLocked() {
	if s.stableCursorSet {
		return
	}
	s.snapshotCursorLocked()
}

func (s *Session) snapshotCursorLocked() {
	position := s.emulator.CursorPosition()
	s.stableCursorX = position.X
	s.stableCursorY = position.Y
	s.stableCursorSet = true
}

func (s *Session) scheduleCursorSettleLocked() {
	s.cursorPending = true
	s.cursorDeadline = time.Now().Add(cursorSettleDelay)
	if s.cursorTimer == nil {
		s.cursorTimer = time.AfterFunc(cursorSettleDelay, s.settleCursor)
		return
	}
	s.cursorTimer.Reset(cursorSettleDelay)
}

func (s *Session) cancelCursorSettleLocked() {
	s.cursorPending = false
	s.cursorDeadline = time.Time{}
	if s.cursorTimer != nil {
		s.cursorTimer.Stop()
		s.cursorTimer = nil
	}
}

func (s *Session) settleCursor() {
	s.mu.Lock()
	if !s.cursorPending || s.sessionClosedLocked() {
		s.mu.Unlock()
		return
	}
	if remaining := time.Until(s.cursorDeadline); remaining > 0 {
		if s.cursorTimer != nil {
			s.cursorTimer.Reset(remaining)
		}
		s.mu.Unlock()
		return
	}
	position := s.emulator.CursorPosition()
	changed := !s.stableCursorSet ||
		position.X != s.stableCursorX || position.Y != s.stableCursorY
	s.stableCursorX = position.X
	s.stableCursorY = position.Y
	s.stableCursorSet = true
	s.cursorPending = false
	s.cursorDeadline = time.Time{}
	s.cursorTimer = nil
	s.mu.Unlock()

	if changed {
		s.notify(Event{SessionID: s.id, Kind: Updated})
	}
}

func (s *Session) sessionClosedLocked() bool {
	if s.closed == nil {
		return false
	}
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

func (s *Session) forwardInput() {
	_, err := io.Copy(s.pseudoterm, s.emulator)
	if err != nil && !isClosedError(err) {
		s.fail(err)
	}
}

func (s *Session) waitForExit() {
	err := s.process.Wait()
	s.mu.Lock()
	if s.state != Failed {
		s.state = Exited
		s.stateErr = err
	}
	s.mu.Unlock()
	s.notify(Event{SessionID: s.id, Kind: ProcessExited, Err: err})
}

func (s *Session) fail(err error) {
	s.mu.Lock()
	if s.state == Failed || s.state == Exited {
		s.mu.Unlock()
		return
	}
	s.state = Failed
	s.stateErr = err
	s.mu.Unlock()
	s.notify(Event{SessionID: s.id, Kind: ProcessFailed, Err: err})
}

func (s *Session) notify(event Event) {
	select {
	case s.events <- event:
	default:
	}
}

func terminalEnvironment(environment []string, overrides map[string]string) []string {
	updated := setEnvironment(
		setEnvironment(
			setEnvironment(environment, "TERM", "xterm-256color"),
			"COLORTERM", "truecolor",
		),
		"TERM_PROGRAM", "split",
	)
	for key, value := range overrides {
		updated = setEnvironment(updated, key, value)
	}
	return updated
}

func setEnvironment(environment []string, key, value string) []string {
	prefix := strings.ToUpper(key) + "="
	updated := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(strings.ToUpper(entry), prefix) {
			continue
		}
		updated = append(updated, entry)
	}
	return append(updated, key+"="+value)
}

func isClosedError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "closed") ||
		strings.Contains(message, "operation has been canceled") ||
		strings.Contains(message, "broken pipe")
}
