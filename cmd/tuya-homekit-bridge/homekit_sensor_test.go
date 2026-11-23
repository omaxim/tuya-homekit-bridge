package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
	"github.com/omaxim/tuya-homekit-bridge/hapclient"
)

func TestHomeKitSensorPolls(t *testing.T) {
	accPub, accPriv, _ := ed25519.GenerateKey(rand.Reader)
	seed := make([]byte, 32)
	rand.Read(seed)
	ctrl := hapclient.Controller{ID: "TEST-CONTROLLER", Seed: seed}

	st := hap.NewMemStore()
	kp, _ := json.Marshal(hap.KeyPair{Public: accPub, Private: accPriv})
	st.Set("keypair", kp)
	st.Set("uuid", []byte("AA:BB:CC:00:11:22"))
	pr, _ := json.Marshal(hap.Pairing{Name: ctrl.ID, PublicKey: ctrl.PublicKey(), Permission: 0})
	st.Set(hex.EncodeToString([]byte(ctrl.ID))+".pairing", pr)

	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	th := accessory.NewTemperatureSensor(accessory.Info{Name: "Sensor"})
	th.TempSensor.CurrentTemperature.SetValue(20.5)
	srv, _ := hap.NewServer(st, th.A)
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

	cfg := HomeKitSensorConfig{AccessoryID: "AA:BB:CC:00:11:22", AccessoryLTPK: hex.EncodeToString(accPub),
		ControllerID: ctrl.ID, ControllerSeed: hex.EncodeToString(seed), Address: addr}
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	got := make(chan float64, 4)
	h := newHomeKitSensor(cfg, func(v float64) { got <- v })
	h.poll(ctx)
	th.TempSensor.CurrentTemperature.SetValue(18)
	h.poll(ctx)
	if a, b := <-got, <-got; a != 20.5 || b != 18 {
		t.Fatalf("got %v, %v", a, b)
	}

	// Connection loss: the next poll reconnects.
	h.sess.Close()
	h.poll(ctx) // fails and drops the session
	h.poll(ctx)
	if v := <-got; v != 18 {
		t.Fatalf("after reconnect got %v", v)
	}
}
