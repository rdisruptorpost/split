package sessionserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"time"

	"split/internal/app"
	"split/internal/terminal"
)

type runtimePeer struct {
	connection net.Conn
	encoder    *json.Encoder
}

type acceptedPeer struct {
	connection net.Conn
	err        error
}

type peerRequest struct {
	peer    *runtimePeer
	request request
	err     error
}

// Run starts the single persistent runtime for a state database. It owns the
// app model, SQLite connection, terminal emulators, ConPTY handles, and child
// processes until an explicit stop request arrives.
func Run(root, statePath string) error {
	endpoint, err := Endpoint(statePath)
	if err != nil {
		return err
	}
	listener, err := listenEndpoint(endpoint)
	if err != nil {
		return fmt.Errorf("listen for Split clients: %w", err)
	}
	defer listener.Close()

	model, err := app.Open(root, statePath)
	if err != nil {
		return err
	}
	defer model.Close()

	accepted := make(chan acceptedPeer, 4)
	requests := make(chan peerRequest, 128)
	go acceptPeers(listener, accepted)

	peers := make(map[*runtimePeer]struct{})
	var active *runtimePeer
	lastFrame := time.Time{}
	lastAgentScan := time.Time{}
	dirty := false
	var pendingClipboard *string
	ticker := time.NewTicker(time.Second / 60)
	defer ticker.Stop()

	closePeer := func(peer *runtimePeer) {
		if peer == nil {
			return
		}
		_, exists := peers[peer]
		if !exists {
			return
		}
		delete(peers, peer)
		_ = peer.connection.Close()
		if active == peer {
			active = nil
			pendingClipboard = nil
			model.ClientDetached()
		}
	}
	send := func(peer *runtimePeer, value frame) bool {
		if peer == nil {
			return false
		}
		_ = peer.connection.SetWriteDeadline(time.Now().Add(2 * time.Second))
		err := peer.encoder.Encode(value)
		_ = peer.connection.SetWriteDeadline(time.Time{})
		if err != nil {
			log.Printf("Split client frame write failed: %v", err)
			closePeer(peer)
			return false
		}
		return true
	}
	sendView := func(peer *runtimePeer, detach bool) bool {
		value := frameFromView(model.View())
		value.Detach = detach
		if pendingClipboard != nil {
			clipboard := *pendingClipboard
			value.Clipboard = &clipboard
		}
		if send(peer, value) {
			pendingClipboard = nil
			lastFrame = time.Now()
			return true
		}
		return false
	}

	for {
		select {
		case result := <-accepted:
			if result.err != nil {
				if errors.Is(result.err, net.ErrClosed) {
					return nil
				}
				return fmt.Errorf("accept Split client: %w", result.err)
			}
			peer := &runtimePeer{connection: result.connection, encoder: json.NewEncoder(result.connection)}
			peers[peer] = struct{}{}
			go readPeerRequests(peer, requests)

		case incoming := <-requests:
			if _, exists := peers[incoming.peer]; !exists {
				continue
			}
			if incoming.err != nil {
				closePeer(incoming.peer)
				continue
			}
			if incoming.request.Version != protocolVersion {
				send(incoming.peer, frame{Version: protocolVersion, Error: "The Split client and runtime use different protocol versions."})
				closePeer(incoming.peer)
				continue
			}

			switch incoming.request.Kind {
			case requestAttach:
				if active != nil && active != incoming.peer {
					sendView(active, true)
					closePeer(active)
				}
				active = incoming.peer
				dirty = false
				sendView(active, false)

			case requestStop:
				// Finish persistence and terminate every ConPTY child before the
				// command acknowledges shutdown to the caller.
				model.Close()
				send(incoming.peer, frame{Version: protocolVersion, Stopped: true})
				_ = listener.Close()
				for peer := range peers {
					_ = peer.connection.Close()
				}
				return nil

			default:
				if incoming.peer != active {
					continue
				}
				message, err := incoming.request.message()
				if err != nil {
					send(active, frame{Version: protocolVersion, Error: err.Error()})
					closePeer(active)
					continue
				}
				_, _ = model.Update(message)
				if clipboard, ok := model.TakeClipboardRequest(); ok {
					pendingClipboard = &clipboard
				}
				if model.TakeDetachRequest() {
					peer := active
					sendView(peer, true)
					closePeer(peer)
					continue
				}
				// Input can arrive much faster than a terminal can draw (wheel
				// bursts are a common example). Apply every state transition,
				// but coalesce the resulting view at the 60 Hz render tick.
				dirty = true
			}

		case event := <-model.TerminalEvents():
			events := []terminal.Event{event}
			for len(events) < 512 {
				select {
				case next := <-model.TerminalEvents():
					events = append(events, next)
				default:
					model.ApplyTerminalEvents(events)
					dirty = true
					events = nil
				}
				if events == nil {
					break
				}
			}
			if events != nil {
				model.ApplyTerminalEvents(events)
				dirty = true
			}

		case now := <-ticker.C:
			if now.Sub(lastAgentScan) >= 250*time.Millisecond {
				lastAgentScan = now
				if model.RefreshAgents(now) {
					dirty = true
				}
			}
			if active == nil {
				continue
			}
			frameInterval := time.Second
			if model.HasAnimatingAgents() {
				frameInterval = 80 * time.Millisecond
			}
			if dirty || now.Sub(lastFrame) >= frameInterval {
				dirty = false
				sendView(active, false)
			}
		}
	}
}

func acceptPeers(listener net.Listener, accepted chan<- acceptedPeer) {
	for {
		connection, err := listener.Accept()
		accepted <- acceptedPeer{connection: connection, err: err}
		if err != nil {
			return
		}
	}
}

func readPeerRequests(peer *runtimePeer, requests chan<- peerRequest) {
	decoder := json.NewDecoder(peer.connection)
	for {
		var next request
		if err := decoder.Decode(&next); err != nil {
			requests <- peerRequest{peer: peer, err: err}
			return
		}
		requests <- peerRequest{peer: peer, request: next}
	}
}
