package jinn

import (
	"context"
	"encoding/binary"
	"errors"
	"net/url"
	"strconv"
)

// Terminal frame kinds carried over TLS WebSocket to wss://ssh.usejinn.com/terminal.
const (
	FrameInput  = byte(1) // keystrokes, caller to relay
	FrameOutput = byte(2) // shell output, relay to caller
	FrameResize = byte(3) // rows and cols, two big-endian uint16s, caller to relay
	FrameExit   = byte(4) // shell exit status, one big-endian int32, relay to caller
)

// TerminalTicket is a single-use short-lived ticket for opening a WebSocket
// terminal session through ssh.usejinn.com.
type TerminalTicket struct {
	Ticket   string `json:"ticket"`
	RelayURL string `json:"relay_url"`
}

// URL returns the full WebSocket URL including ticket and initial terminal size.
func (t TerminalTicket) URL(rows, cols int) string {
	q := url.Values{"ticket": {t.Ticket}, "rows": {strconv.Itoa(rows)}, "cols": {strconv.Itoa(cols)}}
	return t.RelayURL + "?" + q.Encode()
}

// OpenTerminal requests a single-use WebSocket terminal ticket for a running run.
func (c *Client) OpenTerminal(ctx context.Context, run string) (TerminalTicket, error) {
	var t TerminalTicket
	return t, c.do(ctx, "POST", "/v1/runs/"+url.PathEscape(run)+"/terminal", nil, &t)
}

// OpenMachineTerminal requests a single-use WebSocket terminal ticket for a running machine.
func (c *Client) OpenMachineTerminal(ctx context.Context, machine string) (TerminalTicket, error) {
	var t TerminalTicket
	return t, c.do(ctx, "POST", "/v1/machines/"+url.PathEscape(machine)+"/terminal", nil, &t)
}

// EncodeTerminalFrame encodes one plain binary terminal frame ([kind][data]).
func EncodeTerminalFrame(kind byte, data []byte) []byte {
	f := make([]byte, 1+len(data))
	f[0] = kind
	copy(f[1:], data)
	return f
}

// EncodeResizeFrame encodes a terminal resize frame.
func EncodeResizeFrame(rows, cols int) []byte {
	var b [5]byte
	b[0] = FrameResize
	binary.BigEndian.PutUint16(b[1:3], uint16(rows))
	binary.BigEndian.PutUint16(b[3:5], uint16(cols))
	return b[:]
}

// DecodeTerminalFrame splits a binary terminal WebSocket message into kind and payload.
func DecodeTerminalFrame(frame []byte) (byte, []byte, error) {
	if len(frame) == 0 {
		return 0, nil, errors.New("terminal: empty frame")
	}
	return frame[0], frame[1:], nil
}
