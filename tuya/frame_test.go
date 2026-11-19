package tuya

import (
	"bytes"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef")
	payload := []byte(`{"dps":{"1":true}}`)
	f, err := encodeFrame(key, 7, CmdStatus, append(append([]byte{}, version35Header...), payload...))
	if err != nil {
		t.Fatal(err)
	}
	cmd, got, err := decodeFrame(key, f)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != CmdStatus || !bytes.Equal(got, payload) {
		t.Fatalf("got cmd=%d payload=%q", cmd, got)
	}
	if d := parseDPS(got); d["1"] != true {
		t.Fatalf("parseDPS: %v", d)
	}
	if _, _, err := decodeFrame([]byte("fedcba9876543210"), f); err == nil {
		t.Fatal("expected error with wrong key")
	}
}
