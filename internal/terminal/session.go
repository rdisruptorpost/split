package terminal

import (
	"errors"
	"image/color"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aymanbagabas/go-pty"
	uv "github.com/charmbracelet/ultraviolet"
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

type Session struct {
	id      string
	command Command
	events  chan<- Event

	mu           sync.RWMutex
	emulator     *vt.Emulator
	pseudoterm   pty.Pty
	process      *pty.Cmd
	state        State
	stateErr     error
	lastActivity time.Time
	title        string
	cursorShown  bool
	cursorBlink  bool
	cursorStyle  vt.CursorStyle
	outputFilter outputFilter

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

	session := &Session{
		id:           id,
		command:      command,
		events:       events,
		emulator:     emulator,
		pseudoterm:   pseudoterm,
		process:      cmd,
		state:        Starting,
		lastActivity: time.Now(),
		cursorShown:  true,
		cursorBlink:  true,
		cursorStyle:  vt.CursorBlock,
	}
	emulator.SetCallbacks(vt.Callbacks{
		Title: func(title string) {
			session.title = title
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

func DefaultShell(dir string) Command {
	if runtime.GOOS == "windows" {
		if path, err := exec.LookPath("pwsh.exe"); err == nil {
			return Command{Name: path, Args: []string{"-NoLogo"}, Dir: dir}
		}
		if path, err := exec.LookPath("powershell.exe"); err == nil {
			return Command{Name: path, Args: []string{"-NoLogo"}, Dir: dir}
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

func (s *Session) Render() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.emulator.Render()
}

func (s *Session) Cursor() Cursor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	position := s.emulator.CursorPosition()
	return Cursor{
		X:       position.X,
		Y:       position.Y,
		Visible: s.cursorShown,
		Blink:   s.cursorBlink,
		Style:   s.cursorStyle,
		Color:   s.emulator.CursorColor(),
	}
}

func (s *Session) Resize(width, height int) error {
	width = max(2, width)
	height = max(1, height)

	s.mu.Lock()
	s.emulator.Resize(width, height)
	s.mu.Unlock()
	return s.pseudoterm.Resize(width, height)
}

func (s *Session) SendKey(message tea.KeyPressMsg) {
	key := tea.Key(message)
	s.mu.Lock()
	s.emulator.SendKey(uv.KeyPressEvent(key))
	s.mu.Unlock()
}

func (s *Session) Paste(content string) {
	s.mu.Lock()
	s.emulator.Paste(content)
	s.mu.Unlock()
}

func (s *Session) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
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
				_, writeErr = s.emulator.Write(filtered)
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
