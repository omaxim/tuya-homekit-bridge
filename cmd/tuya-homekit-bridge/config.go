package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/omaxim/tuya-homekit-bridge/tuya"
)

// Config is the bridge configuration file (JSON).
type Config struct {
	Name         string     `json:"name"`          // bridge name shown in the Home app
	Pin          string     `json:"pin"`           // 8-digit HomeKit setup code, e.g. "12345678"
	Storage      string     `json:"storage"`       // directory for pairing data (must persist)
	PollInterval Duration   `json:"poll_interval"` // how often to read device state, e.g. "10s"
	Interfaces   []string   `json:"interfaces"`    // optional: network interfaces to advertise on
	Web          *WebConfig `json:"web"`           // optional local web UI
	Devices      []Device   `json:"devices"`
}

// WebConfig enables a small password-protected web UI for the local network.
type WebConfig struct {
	Listen   string `json:"listen"`   // e.g. ":8080"
	Password string `json:"password"` // shared password; empty = no login (not recommended)
	Title    string `json:"title"`    // page title (defaults to the bridge name)
	Lang     string `json:"lang"`     // "en" (default) or "cs"
}

// Device is one Tuya device and the HomeKit accessories it is exposed as.
type Device struct {
	Name        string      `json:"name"`
	ID          string      `json:"id"`        // Tuya device id
	IP          string      `json:"ip"`        // optional; discovered via UDP broadcast if empty
	LocalKey    string      `json:"local_key"` // 16-character local key
	Accessories []Accessory `json:"accessories"`
	Enforce     []Rule      `json:"enforce"`
}

// Accessory describes one HomeKit accessory backed by data points.
//
//	type "switch":      state, on, off
//	type "temperature": dp, scale (value is divided by scale)
type Accessory struct {
	Type  string   `json:"type"`
	Name  string   `json:"name"`
	State tuya.DPS `json:"state"` // switch is "on" when all of these DPs match
	On    tuya.DPS `json:"on"`    // DPs written when switched on
	Off   tuya.DPS `json:"off"`   // DPs written when switched off
	// Optional second write after On/Off, for devices that ignore some DPs
	// in the same command (e.g. a heater that ignores its power level while off).
	OnThen  tuya.DPS `json:"on_then"`
	OffThen tuya.DPS `json:"off_then"`
	DP      string   `json:"dp"`
	Scale   float64  `json:"scale"`
}

// Rule re-applies Set whenever all DPs in When match and Set is not already in effect.
type Rule struct {
	When tuya.DPS `json:"when"`
	Set  tuya.DPS `json:"set"`
}

// Duration is a time.Duration that unmarshals from strings like "10s".
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	d.Duration = v
	return err
}

func loadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.Name == "" {
		c.Name = "Tuya Bridge"
	}
	if c.Storage == "" {
		c.Storage = "db"
	}
	if c.PollInterval.Duration == 0 {
		c.PollInterval.Duration = 10 * time.Second
	}
	if len(c.Pin) != 8 {
		return nil, errors.New("pin must be 8 digits, e.g. \"12345678\" (entered in the Home app as 123-45-678)")
	}
	if len(c.Devices) == 0 {
		return nil, errors.New("no devices configured")
	}
	for i, d := range c.Devices {
		if d.ID == "" || len(d.LocalKey) != 16 {
			return nil, fmt.Errorf("device %d (%s): id and a 16-character local_key are required", i, d.Name)
		}
		for _, a := range d.Accessories {
			switch a.Type {
			case "switch":
				if len(a.State) == 0 || len(a.On) == 0 || len(a.Off) == 0 {
					return nil, fmt.Errorf("switch %q needs state, on and off", a.Name)
				}
			case "temperature":
				if a.DP == "" {
					return nil, fmt.Errorf("temperature %q needs dp", a.Name)
				}
			default:
				return nil, fmt.Errorf("accessory %q: unknown type %q", a.Name, a.Type)
			}
		}
	}
	return &c, nil
}
