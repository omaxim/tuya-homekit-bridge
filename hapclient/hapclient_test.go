package hapclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
)

// TestAgainstBrutellaServer runs a real HAP accessory server in-process and
// exercises pair-verify, reads, AddPairing and a second session with the new key.
func TestAgainstBrutellaServer(t *testing.T) {
	// Accessory identity
	accPub, accPriv, _ := ed25519.GenerateKey(rand.Reader)
	accID := "AA:BB:CC:00:11:22"
	// Admin controller identity
	adminSeed := make([]byte, 32)
	rand.Read(adminSeed)
	admin := Controller{ID: "11111111-2222-3333-4444-555555555555", Seed: adminSeed}

	st := hap.NewMemStore()
	kp, _ := json.Marshal(hap.KeyPair{Public: accPub, Private: accPriv})
	st.Set("keypair", kp)
	st.Set("uuid", []byte(accID))
	pr, _ := json.Marshal(hap.Pairing{Name: admin.ID, PublicKey: admin.PublicKey(), Permission: 1})
	st.Set(hex.EncodeToString([]byte(admin.ID))+".pairing", pr)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	th := accessory.NewTemperatureSensor(accessory.Info{Name: "Sensor"})
	th.TempSensor.CurrentTemperature.SetValue(21.5)
	srv, err := hap.NewServer(st, th.A)
	if err != nil {
		t.Fatal(err)
	}
	srv.Addr = addr
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.ListenAndServe(ctx)
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	acc := Accessory{ID: accID, LTPK: accPub, Addr: addr}

	// Wrong controller must be rejected.
	bad := Controller{ID: "nobody", Seed: make([]byte, 32)}
	if s, err := Dial(acc, bad); err == nil {
		s.Close()
		t.Fatal("unknown controller was accepted")
	}

	s, err := Dial(acc, admin)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.Accessories()
	if err != nil || !strings.Contains(string(raw), "\"accessories\"") {
		t.Fatalf("accessories: %v %s", err, raw)
	}
	tempID, err := FindCharacteristic(raw, "11")
	if err != nil {
		t.Fatal(err)
	}
	vals, err := s.Read(tempID)
	if err != nil || len(vals) != 1 || string(vals[0].Value) != "21.5" {
		t.Fatalf("read: %v %+v", err, vals)
	}

	// Add a regular user controller and use it.
	userSeed := make([]byte, 32)
	rand.Read(userSeed)
	user := Controller{ID: "99999999-8888-7777-6666-555555555555", Seed: userSeed}
	if err := s.AddPairing(user.ID, user.PublicKey(), PermissionUser); err != nil {
		t.Fatal(err)
	}
	// (ListPairings is not exercised: brutella/hap requires an Identifier in
	// list requests, unlike the HAP spec and real accessories.)
	s.Close()

	u, err := Dial(acc, user)
	if err != nil {
		t.Fatalf("user dial: %v", err)
	}
	defer u.Close()
	th.TempSensor.CurrentTemperature.SetValue(19.0)
	vals, err = u.Read(tempID)
	if err != nil || string(vals[0].Value) != "19" {
		t.Fatalf("user read: %v %+v", err, vals)
	}
	if err := u.AddPairing("x", user.PublicKey(), 1); err == nil {
		t.Fatal("non-admin could add a pairing")
	}
}
