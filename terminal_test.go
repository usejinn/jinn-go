package jinn

import (
	"encoding/binary"
	"testing"
)

func TestTerminalFrames(t *testing.T) {
	in := EncodeTerminalFrame(FrameInput, []byte("ls\n"))
	kind, data, err := DecodeTerminalFrame(in)
	if err != nil || kind != FrameInput || string(data) != "ls\n" {
		t.Fatalf("got %d %q %v", kind, data, err)
	}
	rs := EncodeResizeFrame(40, 120)
	kind, data, err = DecodeTerminalFrame(rs)
	if err != nil || kind != FrameResize || len(data) != 4 ||
		binary.BigEndian.Uint16(data[0:2]) != 40 || binary.BigEndian.Uint16(data[2:4]) != 120 {
		t.Fatalf("resize %d %x %v", kind, data, err)
	}
	if _, _, err := DecodeTerminalFrame(nil); err == nil {
		t.Fatal("accepted empty frame")
	}
}
