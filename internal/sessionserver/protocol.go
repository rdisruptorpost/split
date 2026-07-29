package sessionserver

import (
	"errors"
	"image/color"

	tea "charm.land/bubbletea/v2"
)

const protocolVersion = 2

type requestKind string

const (
	requestAttach  requestKind = "attach"
	requestStop    requestKind = "stop"
	requestResize  requestKind = "resize"
	requestKey     requestKind = "key"
	requestPaste   requestKind = "paste"
	requestClick   requestKind = "mouse_click"
	requestRelease requestKind = "mouse_release"
	requestWheel   requestKind = "mouse_wheel"
	requestMotion  requestKind = "mouse_motion"
)

type request struct {
	Version int         `json:"version"`
	Kind    requestKind `json:"kind"`
	Width   int         `json:"width,omitempty"`
	Height  int         `json:"height,omitempty"`
	Key     *tea.Key    `json:"key,omitempty"`
	Mouse   *tea.Mouse  `json:"mouse,omitempty"`
	Paste   string      `json:"paste,omitempty"`
}

type wireColor struct {
	Valid bool  `json:"valid"`
	R     uint8 `json:"r,omitempty"`
	G     uint8 `json:"g,omitempty"`
	B     uint8 `json:"b,omitempty"`
	A     uint8 `json:"a,omitempty"`
}

type wireCursor struct {
	X     int       `json:"x"`
	Y     int       `json:"y"`
	Color wireColor `json:"color"`
	Shape int       `json:"shape"`
	Blink bool      `json:"blink"`
}

type frame struct {
	Version          int         `json:"version"`
	Content          string      `json:"content,omitempty"`
	Cursor           *wireCursor `json:"cursor,omitempty"`
	Background       wireColor   `json:"background"`
	Foreground       wireColor   `json:"foreground"`
	WindowTitle      string      `json:"window_title,omitempty"`
	AltScreen        bool        `json:"alt_screen"`
	ReportFocus      bool        `json:"report_focus"`
	MouseMode        int         `json:"mouse_mode"`
	DisablePasteMode bool        `json:"disable_paste_mode"`
	Clipboard        *string     `json:"clipboard,omitempty"`
	Detach           bool        `json:"detach,omitempty"`
	Stopped          bool        `json:"stopped,omitempty"`
	Error            string      `json:"error,omitempty"`
}

func requestForMessage(message tea.Msg) (request, bool) {
	result := request{Version: protocolVersion}
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		result.Kind = requestResize
		result.Width = message.Width
		result.Height = message.Height
	case tea.KeyPressMsg:
		key := tea.Key(message)
		result.Kind = requestKey
		result.Key = &key
	case tea.PasteMsg:
		result.Kind = requestPaste
		result.Paste = message.Content
	case tea.MouseClickMsg:
		mouse := message.Mouse()
		result.Kind = requestClick
		result.Mouse = &mouse
	case tea.MouseReleaseMsg:
		mouse := message.Mouse()
		result.Kind = requestRelease
		result.Mouse = &mouse
	case tea.MouseWheelMsg:
		mouse := message.Mouse()
		result.Kind = requestWheel
		result.Mouse = &mouse
	case tea.MouseMotionMsg:
		mouse := message.Mouse()
		result.Kind = requestMotion
		result.Mouse = &mouse
	default:
		return request{}, false
	}
	return result, true
}

func (r request) message() (tea.Msg, error) {
	if r.Version != protocolVersion {
		return nil, errors.New("incompatible split runtime protocol")
	}
	switch r.Kind {
	case requestResize:
		return tea.WindowSizeMsg{Width: r.Width, Height: r.Height}, nil
	case requestKey:
		if r.Key == nil {
			return nil, errors.New("key request is missing its key")
		}
		return tea.KeyPressMsg(*r.Key), nil
	case requestPaste:
		return tea.PasteMsg{Content: r.Paste}, nil
	case requestClick:
		if r.Mouse == nil {
			return nil, errors.New("mouse click request is missing its coordinates")
		}
		return tea.MouseClickMsg(*r.Mouse), nil
	case requestRelease:
		if r.Mouse == nil {
			return nil, errors.New("mouse release request is missing its coordinates")
		}
		return tea.MouseReleaseMsg(*r.Mouse), nil
	case requestWheel:
		if r.Mouse == nil {
			return nil, errors.New("mouse wheel request is missing its coordinates")
		}
		return tea.MouseWheelMsg(*r.Mouse), nil
	case requestMotion:
		if r.Mouse == nil {
			return nil, errors.New("mouse motion request is missing its coordinates")
		}
		return tea.MouseMotionMsg(*r.Mouse), nil
	default:
		return nil, errors.New("unknown split runtime request")
	}
}

func frameFromView(view tea.View) frame {
	result := frame{
		Version:          protocolVersion,
		Content:          view.Content,
		Background:       encodeColor(view.BackgroundColor),
		Foreground:       encodeColor(view.ForegroundColor),
		WindowTitle:      view.WindowTitle,
		AltScreen:        view.AltScreen,
		ReportFocus:      view.ReportFocus,
		MouseMode:        int(view.MouseMode),
		DisablePasteMode: view.DisableBracketedPasteMode,
	}
	if view.Cursor != nil {
		result.Cursor = &wireCursor{
			X: view.Cursor.X, Y: view.Cursor.Y,
			Color: encodeColor(view.Cursor.Color),
			Shape: int(view.Cursor.Shape), Blink: view.Cursor.Blink,
		}
	}
	return result
}

func (f frame) view() tea.View {
	view := tea.NewView(f.Content)
	view.BackgroundColor = decodeColor(f.Background)
	view.ForegroundColor = decodeColor(f.Foreground)
	view.WindowTitle = f.WindowTitle
	view.AltScreen = f.AltScreen
	view.ReportFocus = f.ReportFocus
	view.MouseMode = tea.MouseMode(f.MouseMode)
	view.DisableBracketedPasteMode = f.DisablePasteMode
	if f.Cursor != nil {
		cursor := tea.NewCursor(f.Cursor.X, f.Cursor.Y)
		cursor.Color = decodeColor(f.Cursor.Color)
		cursor.Shape = tea.CursorShape(f.Cursor.Shape)
		cursor.Blink = f.Cursor.Blink
		view.Cursor = cursor
	}
	return view
}

func encodeColor(value color.Color) wireColor {
	if value == nil {
		return wireColor{}
	}
	r, g, b, a := value.RGBA()
	return wireColor{Valid: true, R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}
}

func decodeColor(value wireColor) color.Color {
	if !value.Valid {
		return nil
	}
	return color.RGBA{R: value.R, G: value.G, B: value.B, A: value.A}
}
