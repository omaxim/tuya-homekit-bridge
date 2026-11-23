package main

// HomeKit temperature source: the bridge acts as a (non-admin) HomeKit
// controller of a sensor accessory - typically the HomePod's built-in
// temperature sensor - and polls its CurrentTemperature. The pairing is
// created once with cmd/hap-pair (see docs/homepod-sensor.md).

import (
	"context"
	"encoding/hex"
	"errors"
	"log"
	"math"
	"strconv"
	"time"

	"github.com/omaxim/tuya-homekit-bridge/hapclient"
)

// HomeKitSensorConfig is the output of hap-pair plus optional settings.
type HomeKitSensorConfig struct {
	AccessoryID    string   `json:"accessory_id"`
	AccessoryLTPK  string   `json:"accessory_ltpk"`  // 64 hex characters
	ControllerID   string   `json:"controller_id"`   // our pairing identifier
	ControllerSeed string   `json:"controller_seed"` // 64 hex characters, secret
	Address        string   `json:"address"`         // host:port; default: mDNS lookup by accessory_id
	Interval       Duration `json:"interval"`        // default 1m
}

func (c *HomeKitSensorConfig) validate() error {
	if c.AccessoryID == "" || c.ControllerID == "" {
		return errors.New("homekit_sensor: accessory_id and controller_id are required")
	}
	if b, err := hex.DecodeString(c.AccessoryLTPK); err != nil || len(b) != 32 {
		return errors.New("homekit_sensor: accessory_ltpk must be 64 hex characters")
	}
	if b, err := hex.DecodeString(c.ControllerSeed); err != nil || len(b) != 32 {
		return errors.New("homekit_sensor: controller_seed must be 64 hex characters")
	}
	if c.Interval.Duration == 0 {
		c.Interval.Duration = time.Minute
	}
	return nil
}

type homekitSensor struct {
	cfg  HomeKitSensorConfig
	acc  hapclient.Accessory
	ctrl hapclient.Controller
	set  func(float64)

	sess   *hapclient.Session
	charID string
	last   float64
	ok     bool
}

func newHomeKitSensor(c HomeKitSensorConfig, set func(float64)) *homekitSensor {
	ltpk, _ := hex.DecodeString(c.AccessoryLTPK)
	seed, _ := hex.DecodeString(c.ControllerSeed)
	return &homekitSensor{cfg: c, set: set,
		acc:  hapclient.Accessory{ID: c.AccessoryID, LTPK: ltpk, Addr: c.Address},
		ctrl: hapclient.Controller{ID: c.ControllerID, Seed: seed}}
}

func (h *homekitSensor) connect(ctx context.Context) error {
	if h.cfg.Address == "" {
		addr, err := hapclient.Discover(ctx, h.acc.ID, 10*time.Second)
		if err != nil {
			return err
		}
		h.acc.Addr = addr
	}
	s, err := hapclient.Dial(h.acc, h.ctrl)
	if err != nil {
		return err
	}
	if h.charID == "" {
		doc, err := s.Accessories()
		if err == nil {
			h.charID, err = hapclient.FindCharacteristic(doc, "11") // CurrentTemperature
		}
		if err != nil {
			s.Close()
			return err
		}
	}
	log.Printf("homekit sensor: connected to %s at %s", h.acc.ID, h.acc.Addr)
	h.sess = s
	return nil
}

func (h *homekitSensor) read(ctx context.Context) (float64, error) {
	if h.sess == nil {
		if err := h.connect(ctx); err != nil {
			return 0, err
		}
	}
	v, err := h.sess.Read(h.charID)
	if err == nil && len(v) != 1 {
		err = errors.New("unexpected reply")
	}
	if err != nil {
		h.sess.Close()
		h.sess = nil // reconnect (and re-discover the port) next time
		return 0, err
	}
	t, err := strconv.ParseFloat(string(v[0].Value), 64)
	if err != nil || math.IsNaN(t) || t < -30 || t > 60 {
		return 0, errors.New("implausible temperature " + string(v[0].Value))
	}
	return t, nil
}

func (h *homekitSensor) poll(ctx context.Context) {
	t, err := h.read(ctx)
	if err != nil {
		if h.ok {
			log.Printf("homekit sensor: %v", err)
		}
		h.ok = false
		return
	}
	if !h.ok || math.Abs(t-h.last) >= 0.1 {
		log.Printf("homekit sensor: %.1f °C", t)
	}
	h.ok, h.last = true, t
	h.set(t)
}

func (h *homekitSensor) run(ctx context.Context) {
	h.ok = true // log the first error
	h.poll(ctx)
	tk := time.NewTicker(h.cfg.Interval.Duration)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			if h.sess != nil {
				h.sess.Close()
			}
			return
		case <-tk.C:
			h.poll(ctx)
		}
	}
}
