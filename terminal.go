package jinn

import (
	"context"
	"crypto/aes"
	gocipher "crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// A terminal is a shell in a run's VM, reached through Jinn's relay, which
// pairs your connection with the run's host by the session's route and
// carries only ciphertext. OpenTerminal sends a terminal command with your
// half of an ECDH P-256 key exchange and returns the relay's address, the
// route and the channel: connect a WebSocket to RelayURL + "?route=" +
// Route + "&side=caller", send Seal'd frames as binary messages, and Open
// what comes back. Jinn's hosts use this channel too (service/host/terminal.go);
// the console and the TypeScript SDK implement it, and a test vector keeps them equal.
//
// A frame is its kind (one byte), its sequence (eight bytes, from zero in
// each direction) and the sealed data.

// OpenTerminal starts a shell in a run and returns where to reach it and
// the channel to talk through.
func (c *Client) OpenTerminal(ctx context.Context, run string, rows, cols int, env map[string]string) (TerminalTicket, *TerminalChannel, error) {
	private, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return TerminalTicket{}, nil, err
	}
	cmd, err := c.SendCommand(ctx, run, KindTerminal, TerminalArgs{Rows: rows, Cols: cols, PublicKey: EncodeTerminalKey(private.PublicKey()), Env: env})
	if err == nil {
		cmd, err = c.WaitCommand(ctx, run, cmd.ID)
	}
	if err != nil {
		return TerminalTicket{}, nil, err
	}
	if err := commandError(cmd); err != nil {
		return TerminalTicket{}, nil, err
	}
	var t TerminalTicket
	if err := json.Unmarshal(cmd.Result, &t); err != nil {
		return t, nil, err
	}
	peer, err := DecodeTerminalKey(t.PublicKey)
	if err != nil {
		return t, nil, err
	}
	ch, err := NewTerminalChannel(private, peer, true)
	return t, ch, err
}

// Terminal frame kinds.
const (
	FrameInput  = byte(1) // keystrokes, caller to host
	FrameOutput = byte(2) // what the shell wrote, host to caller
	FrameResize = byte(3) // rows and cols, two big-endian uint16s, caller to host
	FrameExit   = byte(4) // the shell's exit status, one big-endian int32, host to caller
)

// MaxTerminalData bounds one frame's data.
const MaxTerminalData = 32 << 10

// TerminalArgs are a terminal command's arguments. OpenTerminal fills
// them in.
type TerminalArgs struct {
	Rows      int               `json:"rows"`
	Cols      int               `json:"cols"`
	PublicKey string            `json:"public_key"` // the caller's ECDH P-256 public key, base64url
	Env       map[string]string `json:"env,omitempty"`
}

// TerminalTicket is a terminal command's result: where to connect, and the
// host's half of the key.
type TerminalTicket struct {
	RelayURL  string `json:"relay_url"`
	Route     string `json:"route"`
	PublicKey string `json:"public_key"`
}

// TerminalChannel seals and opens one end's frames.
type TerminalChannel struct {
	send, recv       gocipher.AEAD
	sendSeq, recvSeq uint64
}

// DecodeTerminalKey reads a base64url ECDH P-256 public key.
func DecodeTerminalKey(s string) (*ecdh.PublicKey, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("public_key is not base64url: %w", err)
	}
	return ecdh.P256().NewPublicKey(b)
}

// EncodeTerminalKey writes a public key as base64url.
func EncodeTerminalKey(k *ecdh.PublicKey) string {
	return base64.RawURLEncoding.EncodeToString(k.Bytes())
}

// NewTerminalChannel derives one end's channel from its private key and the
// other end's public key. caller says which end this is.
func NewTerminalChannel(private *ecdh.PrivateKey, peer *ecdh.PublicKey, caller bool) (*TerminalChannel, error) {
	secret, err := private.ECDH(peer)
	if err != nil {
		return nil, err
	}
	callerKey, hostKey := private.PublicKey().Bytes(), peer.Bytes()
	if !caller {
		callerKey, hostKey = hostKey, callerKey
	}
	salt := sha256.Sum256(append(append([]byte("jinn-terminal-v1\x00"), callerKey...), hostKey...))
	keys, err := hkdf.Key(sha256.New, secret, salt[:], "jinn-terminal-v1 keys", 64)
	if err != nil {
		return nil, err
	}
	toHost, err := gcm(keys[:32])
	if err != nil {
		return nil, err
	}
	toCaller, err := gcm(keys[32:])
	if err != nil {
		return nil, err
	}
	if caller {
		return &TerminalChannel{send: toHost, recv: toCaller}, nil
	}
	return &TerminalChannel{send: toCaller, recv: toHost}, nil
}

func gcm(key []byte) (gocipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return gocipher.NewGCM(block)
}

func terminalNonce(seq uint64) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[4:], seq)
	return n
}

// Seal makes the next frame. Frames go out in order; one end seals from one
// goroutine at a time.
func (c *TerminalChannel) Seal(kind byte, data []byte) []byte {
	head := make([]byte, 9, 9+len(data)+c.send.Overhead())
	head[0] = kind
	binary.BigEndian.PutUint64(head[1:], c.sendSeq)
	frame := c.send.Seal(head, terminalNonce(c.sendSeq), data, append([]byte(nil), head...))
	c.sendSeq++
	return frame
}

// Open reads the next frame: one out of order, replayed or altered is an
// error, and the session should end.
func (c *TerminalChannel) Open(frame []byte) (byte, []byte, error) {
	if len(frame) < 9+c.recv.Overhead() || len(frame) > 9+MaxTerminalData+c.recv.Overhead() {
		return 0, nil, errors.New("terminal: a frame of the wrong size")
	}
	if seq := binary.BigEndian.Uint64(frame[1:9]); seq != c.recvSeq {
		return 0, nil, fmt.Errorf("terminal: frame %d out of order (want %d)", seq, c.recvSeq)
	}
	data, err := c.recv.Open(nil, terminalNonce(c.recvSeq), frame[9:], frame[:9])
	if err != nil {
		return 0, nil, errors.New("terminal: a frame failed authentication")
	}
	c.recvSeq++
	return frame[0], data, nil
}
