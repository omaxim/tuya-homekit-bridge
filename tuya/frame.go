// Package tuya implements a minimal client for the Tuya local LAN protocol 3.5
// (the "6699" frame format with AES-GCM and per-connection session keys).
//
// It is a small, dependency-free port of the relevant parts of tinytuya
// (https://github.com/jasonacox/tinytuya). Older protocol versions (3.1–3.4)
// are not supported.
package tuya

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
)

// Command codes used by protocol 3.5.
const (
	CmdSessKeyStart  = 3
	CmdSessKeyResp   = 4
	CmdSessKeyFinish = 5
	CmdStatus        = 8
	CmdHeartBeat     = 9
	CmdControlNew    = 13
	CmdQueryNew      = 16
	CmdUDPNew        = 0x13
	CmdBroadcastLPV  = 0x23
)

const (
	prefix6699 = 0x00006699
	suffix6699 = 0x00009966
	headerLen  = 18 // prefix(4) unknown(2) seq(4) cmd(4) len(4)
	ivLen      = 12
	tagLen     = 16
	maxFrame   = 16 * 1024
)

// version35Header is prepended to command payloads that carry DPS.
var version35Header = append([]byte("3.5"), make([]byte, 12)...)

func newGCM(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// encodeFrame builds an encrypted 6699 frame.
func encodeFrame(key []byte, seq, cmd uint32, payload []byte) ([]byte, error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	hdr := make([]byte, headerLen)
	binary.BigEndian.PutUint32(hdr[0:], prefix6699)
	binary.BigEndian.PutUint32(hdr[6:], seq)
	binary.BigEndian.PutUint32(hdr[10:], cmd)
	binary.BigEndian.PutUint32(hdr[14:], uint32(len(payload)+ivLen+tagLen))
	iv := make([]byte, ivLen)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	out := append(hdr, iv...)
	out = g.Seal(out, iv, payload, hdr[4:])
	return binary.BigEndian.AppendUint32(out, suffix6699), nil
}

// frameLen returns the total length of the frame starting at buf, or an error
// if the header is invalid. buf must hold at least headerLen bytes.
func frameLen(buf []byte) (int, error) {
	if binary.BigEndian.Uint32(buf) != prefix6699 {
		return 0, errors.New("tuya: unexpected frame prefix (device not using protocol 3.5?)")
	}
	n := int(binary.BigEndian.Uint32(buf[14:]))
	if n < ivLen+tagLen || n > maxFrame {
		return 0, fmt.Errorf("tuya: bad frame length %d", n)
	}
	return headerLen + n + 4, nil
}

// decodeFrame decrypts one complete frame and returns its command and payload.
// A leading 4-byte return code and the "3.5" version header are stripped.
func decodeFrame(key []byte, frame []byte) (uint32, []byte, error) {
	total, err := frameLen(frame)
	if err != nil {
		return 0, nil, err
	}
	if len(frame) < total {
		return 0, nil, errors.New("tuya: short frame")
	}
	g, err := newGCM(key)
	if err != nil {
		return 0, nil, err
	}
	cmd := binary.BigEndian.Uint32(frame[10:])
	body := frame[headerLen : total-4]
	pt, err := g.Open(nil, body[:ivLen], body[ivLen:], frame[4:headerLen])
	if err != nil {
		return cmd, nil, fmt.Errorf("tuya: decrypt failed (wrong local key?): %w", err)
	}
	if len(pt) >= 4 && pt[0] != '{' && !bytes.HasPrefix(pt, []byte("3.5")) {
		pt = pt[4:] // return code
	}
	if bytes.HasPrefix(pt, []byte("3.5")) && len(pt) >= len(version35Header) {
		pt = pt[len(version35Header):]
	}
	return cmd, bytes.TrimRight(pt, "\x00"), nil
}
