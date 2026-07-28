package sessionserver

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"

	tea "charm.land/bubbletea/v2"
)

type frameResult struct {
	frame frame
	err   error
}

// Client is the lightweight Bubble Tea model shown in the user's terminal.
// The detached runtime owns the real app model and every ConPTY process.
type Client struct {
	connection net.Conn
	encoder    *json.Encoder
	frames     chan frameResult

	sendMu    sync.Mutex
	stateMu   sync.RWMutex
	current   frame
	err       error
	closing   bool
	detached  bool
	closeOnce sync.Once
}

func newClient(connection net.Conn) (*Client, error) {
	client := &Client{
		connection: connection,
		encoder:    json.NewEncoder(connection),
		frames:     make(chan frameResult, 4),
	}
	if err := client.send(request{Version: protocolVersion, Kind: requestAttach}); err != nil {
		_ = connection.Close()
		return nil, err
	}
	go client.readFrames()
	return client, nil
}

func (c *Client) Init() tea.Cmd {
	return waitForFrame(c.frames)
}

func (c *Client) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case frameResult:
		if message.err != nil {
			c.stateMu.Lock()
			if !c.closing && !c.detached && !errors.Is(message.err, io.EOF) && !errors.Is(message.err, net.ErrClosed) {
				c.err = message.err
			}
			c.stateMu.Unlock()
			return c, tea.Quit
		}
		c.stateMu.Lock()
		c.current = message.frame
		if message.frame.Detach {
			c.detached = true
		}
		if message.frame.Error != "" {
			c.err = errors.New(message.frame.Error)
		}
		detach := message.frame.Detach
		failed := message.frame.Error != ""
		c.stateMu.Unlock()
		if detach || failed {
			return c, tea.Quit
		}
		return c, waitForFrame(c.frames)
	}

	request, ok := requestForMessage(message)
	if !ok {
		return c, nil
	}
	if err := c.send(request); err != nil {
		c.stateMu.Lock()
		c.err = err
		c.stateMu.Unlock()
		return c, tea.Quit
	}
	return c, nil
}

func (c *Client) View() tea.View {
	c.stateMu.RLock()
	current := c.current
	c.stateMu.RUnlock()
	if current.Version == protocolVersion {
		return current.view()
	}
	view := tea.NewView("Connecting to the Split runtime\u2026")
	view.AltScreen = true
	return view
}

func (c *Client) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		c.stateMu.Lock()
		c.closing = true
		c.stateMu.Unlock()
		closeErr = c.connection.Close()
	})
	return closeErr
}

func (c *Client) Err() error {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.err
}

func (c *Client) send(value request) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.encoder.Encode(value)
}

func (c *Client) readFrames() {
	decoder := json.NewDecoder(c.connection)
	for {
		var next frame
		if err := decoder.Decode(&next); err != nil {
			c.frames <- frameResult{err: err}
			return
		}
		c.frames <- frameResult{frame: next}
	}
}

func waitForFrame(frames <-chan frameResult) tea.Cmd {
	return func() tea.Msg {
		return <-frames
	}
}
