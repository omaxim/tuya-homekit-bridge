package tuya

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Device is a Tuya device reachable on the local network.
// It opens a short-lived TCP connection per operation, which is robust against
// devices that drop idle connections. Methods are safe for concurrent use.
type Device struct {
	ID       string        // device id (gwId); informational only
	Addr     string        // IP address or host name
	LocalKey string        // 16-character local key
	Timeout  time.Duration // per-operation I/O timeout (default 5s)

	mu sync.Mutex
}

// DPS maps data point ids ("1", "2", ...) to values (bool, float64, string).
type DPS map[string]any

type conn struct {
	c       net.Conn
	key     []byte
	seq     uint32
	timeout time.Duration
}

func (c *conn) send(cmd uint32, payload []byte) error {
	c.seq++
	f, err := encodeFrame(c.key, c.seq, cmd, payload)
	if err != nil {
		return err
	}
	c.c.SetWriteDeadline(time.Now().Add(c.timeout))
	_, err = c.c.Write(f)
	return err
}

func (c *conn) recv() (uint32, []byte, error) {
	c.c.SetReadDeadline(time.Now().Add(c.timeout))
	hdr := make([]byte, headerLen)
	if _, err := io.ReadFull(c.c, hdr); err != nil {
		return 0, nil, err
	}
	n, err := frameLen(hdr)
	if err != nil {
		return 0, nil, err
	}
	frame := make([]byte, n)
	copy(frame, hdr)
	if _, err := io.ReadFull(c.c, frame[headerLen:]); err != nil {
		return 0, nil, err
	}
	return decodeFrame(c.key, frame)
}

func (d *Device) timeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}
	return 5 * time.Second
}

// dial connects and negotiates a session key.
func (d *Device) dial() (*conn, error) {
	key := []byte(d.LocalKey)
	if len(key) != 16 {
		return nil, errors.New("tuya: local key must be 16 characters")
	}
	nc, err := net.DialTimeout("tcp", net.JoinHostPort(d.Addr, "6668"), d.timeout())
	if err != nil {
		return nil, err
	}
	c := &conn{c: nc, key: key, timeout: d.timeout()}
	fail := func(err error) (*conn, error) { nc.Close(); return nil, err }

	local := make([]byte, 16)
	if _, err := rand.Read(local); err != nil {
		return fail(err)
	}
	if err := c.send(CmdSessKeyStart, local); err != nil {
		return fail(err)
	}
	cmd, p, err := c.recv()
	if err != nil {
		return fail(fmt.Errorf("tuya: session negotiation: %w", err))
	}
	if cmd != CmdSessKeyResp || len(p) < 48 {
		return fail(fmt.Errorf("tuya: unexpected session response (cmd %d, %d bytes)", cmd, len(p)))
	}
	remote := p[:16]
	if !hmac.Equal(hmacSHA256(key, local), p[16:48]) {
		return fail(errors.New("tuya: session HMAC mismatch (wrong local key?)"))
	}
	if err := c.send(CmdSessKeyFinish, hmacSHA256(key, remote)); err != nil {
		return fail(err)
	}
	x := make([]byte, 16)
	for i := range x {
		x[i] = local[i] ^ remote[i]
	}
	g, err := newGCM(key)
	if err != nil {
		return fail(err)
	}
	c.key = g.Seal(nil, local[:12], x, nil)[:16]
	return c, nil
}

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

func parseDPS(p []byte) DPS {
	i := bytes.IndexByte(p, '{')
	if i < 0 {
		return nil
	}
	var m struct {
		DPS  DPS `json:"dps"`
		Data struct {
			DPS DPS `json:"dps"`
		} `json:"data"`
	}
	if json.Unmarshal(p[i:], &m) != nil {
		return nil
	}
	if m.DPS != nil {
		return m.DPS
	}
	return m.Data.DPS
}

// Status returns all current data points.
func (d *Device) Status() (DPS, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, err := d.dial()
	if err != nil {
		return nil, err
	}
	defer c.c.Close()
	if err := c.send(CmdQueryNew, []byte("{}")); err != nil {
		return nil, err
	}
	for i := 0; i < 4; i++ {
		_, p, err := c.recv()
		if err != nil {
			return nil, err
		}
		if dps := parseDPS(p); dps != nil {
			return dps, nil
		}
	}
	return nil, errors.New("tuya: no status in response")
}

// Set writes one or more data points.
func (d *Device) Set(dps DPS) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, err := d.dial()
	if err != nil {
		return err
	}
	defer c.c.Close()
	body, err := json.Marshal(map[string]any{
		"protocol": 5,
		"t":        time.Now().Unix(),
		"data":     map[string]any{"dps": dps},
	})
	if err != nil {
		return err
	}
	if err := c.send(CmdControlNew, append(append([]byte{}, version35Header...), body...)); err != nil {
		return err
	}
	// Wait briefly for the acknowledgement or status push; not all devices send one.
	for i := 0; i < 3; i++ {
		cmd, _, err := c.recv()
		if err != nil || cmd == CmdStatus || cmd == CmdControlNew {
			break
		}
	}
	return nil
}
