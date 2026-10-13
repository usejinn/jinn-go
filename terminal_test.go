package jinn

import (
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// The channel's test vector, the same as service/terminal_test.go's and
// ui/terminal.test.mjs's, so the CLI, the host and the console agree.
const (
	vectorCallerKey = "BGOmXGIuNXXScxSZ6XmQkimNWV0Eln18A6TCJeH-uKAQCGmgfusQCN41WuAADoO2Ma_6IiPOfnPLcU_Y213fpIU"
	vectorHostKey   = "BJTKikBVEn7T_SwODdeALVNWEgGzMxILOgq2z3pDgdNJMWih1H7JRArE8C_I6ZCrTERJU0xfwZCM3xm93jJRmgQ"
	vectorInput     = "01000000000000000070090067983f4a37a088b3e5d2701a50deb9dd" // the caller's first frame: input "ls\n"
	vectorOutput    = "020000000000000000ed87304281a05e0b1784e209e91cb1227edf"   // the host's first frame: output "hi"
)

func vectorKeys(t *testing.T) (caller, host *ecdh.PrivateKey) {
	c, h := sha256.Sum256([]byte("caller")), sha256.Sum256([]byte("host"))
	var err error
	if caller, err = ecdh.P256().NewPrivateKey(c[:]); err != nil {
		t.Fatal(err)
	}
	if host, err = ecdh.P256().NewPrivateKey(h[:]); err != nil {
		t.Fatal(err)
	}
	return caller, host
}

func TestTerminalChannel(t *testing.T) {
	caller, host := vectorKeys(t)
	if EncodeTerminalKey(caller.PublicKey()) != vectorCallerKey || EncodeTerminalKey(host.PublicKey()) != vectorHostKey {
		t.Fatal("the vector's public keys moved")
	}
	hostKey, err := DecodeTerminalKey(vectorHostKey)
	if err != nil {
		t.Fatal(err)
	}
	cc, err := NewTerminalChannel(caller, hostKey, true)
	if err != nil {
		t.Fatal(err)
	}
	hc, err := NewTerminalChannel(host, caller.PublicKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	in := cc.Seal(FrameInput, []byte("ls\n"))
	if hex.EncodeToString(in) != vectorInput {
		t.Fatalf("input frame %x", in)
	}
	out := hc.Seal(FrameOutput, []byte("hi"))
	if hex.EncodeToString(out) != vectorOutput {
		t.Fatalf("output frame %x", out)
	}
	if kind, data, err := hc.Open(in); err != nil || kind != FrameInput || string(data) != "ls\n" {
		t.Fatalf("host opened %d %q %v", kind, data, err)
	}
	if kind, data, err := cc.Open(out); err != nil || kind != FrameOutput || string(data) != "hi" {
		t.Fatalf("caller opened %d %q %v", kind, data, err)
	}
	// A replayed, reordered or altered frame is refused.
	if _, _, err := hc.Open(in); err == nil {
		t.Fatal("a replayed frame was opened")
	}
	next := cc.Seal(FrameInput, []byte("x"))
	next[len(next)-1] ^= 1
	if _, _, err := hc.Open(next); err == nil {
		t.Fatal("an altered frame was opened")
	}
}
