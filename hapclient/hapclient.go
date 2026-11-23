// Package hapclient is a minimal HomeKit Accessory Protocol (HAP) *controller*
// for IP accessories: pair-verify with an existing pairing, the encrypted
// session, reading characteristics and adding/listing pairings.
//
// It does not implement pair-setup (SRP); it only works with a controller
// identity that the accessory already knows (or one added via AddPairing).
package hapclient

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brutella/hap/chacha20poly1305"
	"github.com/brutella/hap/curve25519"
	"github.com/brutella/hap/hkdf"
	"github.com/brutella/hap/tlv8"
)

// Controller is a controller identity: pairing id and Ed25519 key.
type Controller struct {
	ID   string
	Seed []byte // 32-byte Ed25519 seed (iOSDeviceLTSK)
}

func (c Controller) key() ed25519.PrivateKey { return ed25519.NewKeyFromSeed(c.Seed) }

// PublicKey returns the controller's 32-byte Ed25519 public key (LTPK).
func (c Controller) PublicKey() []byte { return c.key().Public().(ed25519.PublicKey) }

// Accessory identifies the accessory and where to reach it.
type Accessory struct {
	ID   string // AccessoryPairingID, e.g. "AA:BB:CC:DD:EE:FF"
	LTPK []byte // accessory Ed25519 public key (32 bytes)
	Addr string // host:port
}

// Session is an authenticated, encrypted connection to an accessory.
type Session struct {
	conn   net.Conn
	mu     sync.Mutex
	encKey [32]byte
	decKey [32]byte
	encN   uint64
	decN   uint64
	rbuf   bytes.Buffer
	br     *bufio.Reader
}

const (
	stateM1, stateM2, stateM3, stateM4 = 1, 2, 3, 4
	timeout                            = 8 * time.Second
)

type tlvVerify struct {
	State         byte   `tlv8:"6,optional"`
	PublicKey     []byte `tlv8:"3,optional"`
	EncryptedData []byte `tlv8:"5,optional"`
	Error         byte   `tlv8:"7,optional"`
}

// The tlv8 encoder ignores ",optional" tags, so outgoing messages use plain tags.
type outM1 struct {
	State     byte   `tlv8:"6"`
	PublicKey []byte `tlv8:"3"`
}

type outM3 struct {
	State         byte   `tlv8:"6"`
	EncryptedData []byte `tlv8:"5"`
}

type outAddPairing struct {
	State      byte   `tlv8:"6"`
	Method     byte   `tlv8:"0"`
	Identifier string `tlv8:"1"`
	PublicKey  []byte `tlv8:"3"`
	Permission byte   `tlv8:"11"`
}

type outListPairings struct {
	State  byte `tlv8:"6"`
	Method byte `tlv8:"0"`
}

type tlvSub struct {
	Identifier string `tlv8:"1"`
	Signature  []byte `tlv8:"10"`
}

// Dial connects and performs pair-verify.
func Dial(acc Accessory, ctrl Controller) (*Session, error) {
	if len(ctrl.Seed) != ed25519.SeedSize || len(acc.LTPK) != ed25519.PublicKeySize {
		return nil, errors.New("hapclient: bad key lengths")
	}
	c, err := net.DialTimeout("tcp", acc.Addr, timeout)
	if err != nil {
		return nil, err
	}
	s := &Session{conn: c}
	if err := s.verify(acc, ctrl); err != nil {
		c.Close()
		return nil, err
	}
	return s, nil
}

// plainPost sends an unencrypted HTTP POST (only used during pair-verify).
func plainPost(c net.Conn, br *bufio.Reader, path string, body []byte) ([]byte, error) {
	c.SetDeadline(time.Now().Add(timeout))
	req := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: hap\r\nContent-Type: application/pairing+tlv8\r\nContent-Length: %d\r\n\r\n", path, len(body))
	if _, err := c.Write(append([]byte(req), body...)); err != nil {
		return nil, err
	}
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (s *Session) verify(acc Accessory, ctrl Controller) error {
	br := bufio.NewReader(s.conn)
	myPub, myPriv := curve25519.GenerateKeyPair()

	m1, _ := tlv8.Marshal(outM1{State: stateM1, PublicKey: myPub[:]})
	raw, err := plainPost(s.conn, br, "/pair-verify", m1)
	if err != nil {
		return fmt.Errorf("pair-verify M1: %w", err)
	}
	var m2 tlvVerify
	if err := tlv8.Unmarshal(raw, &m2); err != nil || m2.State != stateM2 || m2.Error != 0 || len(m2.PublicKey) != 32 || len(m2.EncryptedData) < 16 {
		return fmt.Errorf("pair-verify M2: unexpected reply (error %d)", m2.Error)
	}
	var accEph [32]byte
	copy(accEph[:], m2.PublicKey)
	shared := curve25519.SharedSecret(myPriv, accEph)
	encKey, err := hkdf.Sha512(shared[:], []byte("Pair-Verify-Encrypt-Salt"), []byte("Pair-Verify-Encrypt-Info"))
	if err != nil {
		return err
	}
	msg := m2.EncryptedData[:len(m2.EncryptedData)-16]
	var mac [16]byte
	copy(mac[:], m2.EncryptedData[len(msg):])
	plain, err := chacha20poly1305.DecryptAndVerify(encKey[:], []byte("PV-Msg02"), msg, mac, nil)
	if err != nil {
		return fmt.Errorf("pair-verify M2 decrypt: %w", err)
	}
	var sub tlvSub
	if err := tlv8.Unmarshal(plain, &sub); err != nil {
		return err
	}
	if !strings.EqualFold(sub.Identifier, acc.ID) {
		return fmt.Errorf("pair-verify: accessory id %q does not match %q", sub.Identifier, acc.ID)
	}
	signed := append(append(append([]byte{}, accEph[:]...), []byte(sub.Identifier)...), myPub[:]...)
	if !ed25519.Verify(acc.LTPK, signed, sub.Signature) {
		return errors.New("pair-verify: accessory signature invalid")
	}

	mine := append(append(append([]byte{}, myPub[:]...), []byte(ctrl.ID)...), accEph[:]...)
	inner, _ := tlv8.Marshal(tlvSub{Identifier: ctrl.ID, Signature: ed25519.Sign(ctrl.key(), mine)})
	enc, tag, err := chacha20poly1305.EncryptAndSeal(encKey[:], []byte("PV-Msg03"), inner, nil)
	if err != nil {
		return err
	}
	m3, _ := tlv8.Marshal(outM3{State: stateM3, EncryptedData: append(enc, tag[:]...)})
	raw, err = plainPost(s.conn, br, "/pair-verify", m3)
	if err != nil {
		return fmt.Errorf("pair-verify M3: %w", err)
	}
	var m4 tlvVerify
	if err := tlv8.Unmarshal(raw, &m4); err != nil || m4.State != stateM4 || m4.Error != 0 {
		return fmt.Errorf("pair-verify M4: rejected (error %d) – controller not paired?", m4.Error)
	}
	if s.encKey, err = hkdf.Sha512(shared[:], []byte("Control-Salt"), []byte("Control-Write-Encryption-Key")); err != nil {
		return err
	}
	if s.decKey, err = hkdf.Sha512(shared[:], []byte("Control-Salt"), []byte("Control-Read-Encryption-Key")); err != nil {
		return err
	}
	s.br = bufio.NewReader(readerFunc(s.readDecrypted))
	return nil
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(b []byte) (int, error) { return f(b) }

func nonce(n uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, n)
	return b
}

func (s *Session) writeEncrypted(p []byte) error {
	for len(p) > 0 {
		n := min(len(p), 1024)
		lb := make([]byte, 2)
		binary.LittleEndian.PutUint16(lb, uint16(n))
		enc, tag, err := chacha20poly1305.EncryptAndSeal(s.encKey[:], nonce(s.encN), p[:n], lb)
		if err != nil {
			return err
		}
		s.encN++
		frame := append(append(lb, enc...), tag[:]...)
		if _, err := s.conn.Write(frame); err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}

func (s *Session) readDecrypted(b []byte) (int, error) {
	if s.rbuf.Len() == 0 {
		lb := make([]byte, 2)
		if _, err := io.ReadFull(s.conn, lb); err != nil {
			return 0, err
		}
		n := int(binary.LittleEndian.Uint16(lb))
		frame := make([]byte, n+16)
		if _, err := io.ReadFull(s.conn, frame); err != nil {
			return 0, err
		}
		var tag [16]byte
		copy(tag[:], frame[n:])
		plain, err := chacha20poly1305.DecryptAndVerify(s.decKey[:], nonce(s.decN), frame[:n], tag, lb)
		if err != nil {
			return 0, fmt.Errorf("hapclient: decrypt: %w", err)
		}
		s.decN++
		s.rbuf.Write(plain)
	}
	return s.rbuf.Read(b)
}

// Do sends an encrypted HTTP request and returns status and body.
// EVENT messages that arrive in between are skipped.
func (s *Session) Do(method, path, ctype string, body []byte) (int, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn.SetDeadline(time.Now().Add(timeout))
	var hdr strings.Builder
	fmt.Fprintf(&hdr, "%s %s HTTP/1.1\r\nHost: hap\r\n", method, path)
	if body != nil {
		fmt.Fprintf(&hdr, "Content-Type: %s\r\nContent-Length: %d\r\n", ctype, len(body))
	}
	hdr.WriteString("\r\n")
	if err := s.writeEncrypted(append([]byte(hdr.String()), body...)); err != nil {
		return 0, nil, err
	}
	for {
		// HAP accessories answer with "HTTP/1.1"; notifications start with "EVENT/1.0".
		peek, err := s.br.Peek(5)
		if err != nil {
			return 0, nil, err
		}
		if string(peek) == "EVENT" {
			if err := s.skipEvent(); err != nil {
				return 0, nil, err
			}
			continue
		}
		resp, err := http.ReadResponse(s.br, nil)
		if err != nil {
			return 0, nil, err
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, b, err
	}
}

func (s *Session) skipEvent() error {
	length := 0
	for {
		line, err := s.br.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "content-length") {
			length, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	_, err := s.br.Discard(length)
	return err
}

// Close closes the connection.
func (s *Session) Close() error { return s.conn.Close() }

// Accessories returns the raw /accessories JSON.
func (s *Session) Accessories() ([]byte, error) {
	code, b, err := s.Do("GET", "/accessories", "", nil)
	if err == nil && code != 200 {
		err = fmt.Errorf("hapclient: /accessories returned %d", code)
	}
	return b, err
}

// Characteristic value read from GET /characteristics.
type Characteristic struct {
	AID   uint64          `json:"aid"`
	IID   uint64          `json:"iid"`
	Value json.RawMessage `json:"value"`
}

// Read reads characteristics given as "aid.iid" ids.
func (s *Session) Read(ids ...string) ([]Characteristic, error) {
	code, b, err := s.Do("GET", "/characteristics?id="+strings.Join(ids, ","), "", nil)
	if err != nil {
		return nil, err
	}
	if code != 200 && code != 207 {
		return nil, fmt.Errorf("hapclient: /characteristics returned %d: %s", code, b)
	}
	var out struct {
		Characteristics []Characteristic `json:"characteristics"`
	}
	err = json.Unmarshal(b, &out)
	return out.Characteristics, err
}

type tlvPairings struct {
	State      byte   `tlv8:"6,optional"`
	Method     byte   `tlv8:"0,optional"`
	Identifier string `tlv8:"1,optional"`
	PublicKey  []byte `tlv8:"3,optional"`
	Permission byte   `tlv8:"11,optional"`
	Error      byte   `tlv8:"7,optional"`
}

const (
	methodAddPairing  = 3
	methodListPairing = 5
	// PermissionUser is a regular (non-admin) controller.
	PermissionUser = 0
)

// AddPairing adds a controller identity (requires an admin session).
func (s *Session) AddPairing(id string, ltpk []byte, permission byte) error {
	body, _ := tlv8.Marshal(outAddPairing{State: 1, Method: methodAddPairing, Identifier: id, PublicKey: ltpk, Permission: permission})
	code, b, err := s.Do("POST", "/pairings", "application/pairing+tlv8", body)
	if err != nil {
		return err
	}
	var r tlvPairings
	_ = tlv8.Unmarshal(b, &r)
	if code != 200 || r.Error != 0 {
		return fmt.Errorf("hapclient: add pairing failed (http %d, tlv error %d)", code, r.Error)
	}
	return nil
}

// Pairing is one row of the accessory's pairing list.
type Pairing struct {
	ID         string
	PublicKey  []byte
	Permission byte
}

// ListPairings lists controller pairings (requires an admin session).
func (s *Session) ListPairings() ([]Pairing, error) {
	body, _ := tlv8.Marshal(outListPairings{State: 1, Method: methodListPairing})
	code, b, err := s.Do("POST", "/pairings", "application/pairing+tlv8", body)
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("hapclient: list pairings returned %d", code)
	}
	return parsePairingList(b), nil
}

// parsePairingList parses a TLV list where entries are separated by 0xff items.
func parsePairingList(b []byte) []Pairing {
	var out []Pairing
	cur := Pairing{}
	have := false
	for len(b) >= 2 {
		t, l := b[0], int(b[1])
		if len(b) < 2+l {
			break
		}
		v := b[2 : 2+l]
		b = b[2+l:]
		switch t {
		case 0x01:
			cur.ID += string(v) // ids > 255 bytes would be fragmented
			have = true
		case 0x03:
			cur.PublicKey = append(cur.PublicKey, v...)
		case 0x0b:
			if len(v) > 0 {
				cur.Permission = v[0]
			}
		case 0xff:
			if have {
				out = append(out, cur)
			}
			cur, have = Pairing{}, false
		}
	}
	if have {
		out = append(out, cur)
	}
	return out
}
