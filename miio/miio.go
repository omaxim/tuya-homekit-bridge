// Package miio implements a minimal client for the Xiaomi miIO LAN protocol
// (UDP 54321, AES-128-CBC with a key derived from the 32-hex-character token),
// as used by Mi Home / Yeelight devices. Port of the relevant parts of
// python-miio (https://github.com/rytilahti/python-miio).
package miio

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	port      = 54321
	headerLen = 32
	magic     = 0x2131
)

// Device is a miIO device on the LAN. Methods are safe for concurrent use.
type Device struct {
	Addr    string        // IP address
	Token   string        // 32 hex characters
	Timeout time.Duration // per attempt (default 5s)
	Retries int           // default 2

	mu       sync.Mutex
	key, iv  []byte
	devID    uint32
	stamp    uint32
	stampAt  time.Time
	msgID    int
	tokenRaw []byte
}

func (d *Device) init() error {
	if d.key != nil {
		return nil
	}
	tok, err := hex.DecodeString(d.Token)
	if err != nil || len(tok) != 16 {
		return errors.New("miio: token must be 32 hex characters")
	}
	k := md5.Sum(tok)
	iv := md5.Sum(append(k[:], tok...))
	d.tokenRaw, d.key, d.iv = tok, k[:], iv[:]
	if d.Timeout == 0 {
		d.Timeout = 5 * time.Second
	}
	if d.Retries == 0 {
		d.Retries = 2
	}
	return nil
}

func (d *Device) encrypt(plain []byte) ([]byte, error) {
	b, err := aes.NewCipher(d.key)
	if err != nil {
		return nil, err
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(b, d.iv).CryptBlocks(out, plain)
	return out, nil
}

func (d *Device) decrypt(enc []byte) ([]byte, error) {
	if len(enc) == 0 || len(enc)%aes.BlockSize != 0 {
		return nil, errors.New("miio: bad ciphertext length")
	}
	b, err := aes.NewCipher(d.key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(enc))
	cipher.NewCBCDecrypter(b, d.iv).CryptBlocks(out, enc)
	pad := int(out[len(out)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(out) {
		return nil, errors.New("miio: bad padding (wrong token?)")
	}
	return out[:len(out)-pad], nil
}

func (d *Device) packet(payload []byte) ([]byte, error) {
	enc, err := d.encrypt(payload)
	if err != nil {
		return nil, err
	}
	stamp := d.stamp + uint32(time.Since(d.stampAt).Seconds())
	h := make([]byte, headerLen)
	binary.BigEndian.PutUint16(h[0:], magic)
	binary.BigEndian.PutUint16(h[2:], uint16(headerLen+len(enc)))
	binary.BigEndian.PutUint32(h[8:], d.devID)
	binary.BigEndian.PutUint32(h[12:], stamp)
	copy(h[16:], d.tokenRaw) // placeholder for checksum
	sum := md5.Sum(append(append([]byte{}, h...), enc...))
	copy(h[16:], sum[:])
	return append(h, enc...), nil
}

// hello performs the handshake that returns the device id and time stamp.
func (d *Device) hello(c *net.UDPConn) error {
	pkt := append([]byte{0x21, 0x31, 0x00, 0x20}, bytes.Repeat([]byte{0xff}, 28)...)
	c.SetDeadline(time.Now().Add(d.Timeout))
	if _, err := c.Write(pkt); err != nil {
		return err
	}
	buf := make([]byte, 1024)
	n, err := c.Read(buf)
	if err != nil {
		return fmt.Errorf("miio: no handshake reply from %s: %w", d.Addr, err)
	}
	if n < headerLen || binary.BigEndian.Uint16(buf) != magic {
		return errors.New("miio: bad handshake reply")
	}
	d.devID = binary.BigEndian.Uint32(buf[8:])
	d.stamp = binary.BigEndian.Uint32(buf[12:])
	d.stampAt = time.Now()
	return nil
}

// Send calls a method and returns its "result" field.
func (d *Device) Send(method string, params any) (json.RawMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.init(); err != nil {
		return nil, err
	}
	if params == nil {
		params = []any{}
	}
	var last error
	for attempt := 0; attempt <= d.Retries; attempt++ {
		res, err := d.sendOnce(method, params)
		if err == nil {
			return res, nil
		}
		last = err
		d.stampAt = time.Time{} // force a new handshake
	}
	return nil, last
}

func (d *Device) sendOnce(method string, params any) (json.RawMessage, error) {
	c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP(d.Addr), Port: port})
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if d.stampAt.IsZero() || time.Since(d.stampAt) > 2*time.Minute {
		if err := d.hello(c); err != nil {
			return nil, err
		}
	}
	d.msgID++
	id := d.msgID
	payload, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	pkt, err := d.packet(payload)
	if err != nil {
		return nil, err
	}
	c.SetDeadline(time.Now().Add(d.Timeout))
	if _, err := c.Write(pkt); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	for {
		n, err := c.Read(buf)
		if err != nil {
			return nil, fmt.Errorf("miio: %s: %w", method, err)
		}
		if n <= headerLen {
			continue
		}
		plain, err := d.decrypt(buf[headerLen:n])
		if err != nil {
			return nil, err
		}
		var resp struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(bytes.TrimRight(plain, "\x00"), &resp); err != nil {
			return nil, fmt.Errorf("miio: bad JSON reply: %w", err)
		}
		if resp.ID != id {
			continue // stale reply
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("miio: %s: %s (%d)", method, resp.Error.Message, resp.Error.Code)
		}
		return resp.Result, nil
	}
}
