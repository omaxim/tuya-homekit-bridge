package main

// Thermostat: regulates a Tuya device in stages (off / heat / boost) from a room
// temperature that is pushed to the bridge over HTTP – e.g. by a Home app
// automation running on a HomePod, which reads the HomePod's own temperature
// sensor (third-party software cannot read that sensor directly).

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/omaxim/tuya-homekit-bridge/tuya"
)

// ThermostatConfig configures the optional thermostat.
type ThermostatConfig struct {
	Name          string         `json:"name"`
	Device        int            `json:"device"`         // index into devices
	DefaultTarget float64        `json:"default_target"` // °C, default 21
	MinTarget     float64        `json:"min_target"`     // default 10
	MaxTarget     float64        `json:"max_target"`     // default 28
	Hysteresis    float64        `json:"hysteresis"`     // °C around the target, default 0.3
	BoostBelow    float64        `json:"boost_below"`    // use boost when this far below target, default 1.0; needs "boost"
	MaxAge        Duration       `json:"max_age"`        // turn off if no temperature for this long, default 20m
	Token         string         `json:"token"`          // required for POST/GET /api/temperature
	Off           Stage          `json:"off"`
	Heat          Stage          `json:"heat"`
	Boost         *Stage         `json:"boost"`
	Trigger       *TriggerConfig `json:"trigger"`
}

// Stage is what is written to the device for one regulation stage.
type Stage struct {
	Set  tuya.DPS `json:"set"`
	Then tuya.DPS `json:"then"` // optional second write (see on_then)
}

// TriggerConfig adds a motion sensor that "detects motion" every Interval so a
// Home automation can be started periodically (Home has no timer trigger).
type TriggerConfig struct {
	Name     string   `json:"name"`
	Interval Duration `json:"interval"` // default 5m
}

func (c *ThermostatConfig) setDefaults(cfg *Config) error {
	if c.Name == "" {
		c.Name = "Thermostat"
	}
	if c.Device < 0 || c.Device >= len(cfg.Devices) {
		return fmt.Errorf("thermostat: device %d does not exist", c.Device)
	}
	if c.DefaultTarget == 0 {
		c.DefaultTarget = 21
	}
	if c.MinTarget == 0 {
		c.MinTarget = 10
	}
	if c.MaxTarget == 0 {
		c.MaxTarget = 28
	}
	if c.Hysteresis == 0 {
		c.Hysteresis = 0.3
	}
	if c.BoostBelow == 0 {
		c.BoostBelow = 1.0
	}
	if c.MaxAge.Duration == 0 {
		c.MaxAge.Duration = 20 * time.Minute
	}
	if len(c.Token) < 12 {
		return errors.New("thermostat: token must be at least 12 characters")
	}
	if len(c.Off.Set) == 0 || len(c.Heat.Set) == 0 {
		return errors.New("thermostat: \"off\" and \"heat\" stages are required")
	}
	if c.Trigger != nil {
		if c.Trigger.Name == "" {
			c.Trigger.Name = c.Name + " trigger"
		}
		if c.Trigger.Interval.Duration == 0 {
			c.Trigger.Interval.Duration = 5 * time.Minute
		}
	}
	return nil
}

type stage int

const (
	stageOff stage = iota
	stageHeat
	stageBoost
)

func (s stage) String() string { return [...]string{"off", "heat", "boost"}[s] }

// decide returns the stage for the given conditions. err = target - temperature.
//
//	err >= boostBelow             -> boost (if available)
//	err >= hyst                   -> heat
//	err <= -hyst                  -> off
//	otherwise (dead band)         -> keep previous, but never stay in boost
//
// Boost is kept until the room is within boostBelow-hyst of the target.
func decide(prev stage, enabled, fresh bool, temp, target, hyst, boostBelow float64, hasBoost bool) stage {
	if !enabled || !fresh {
		return stageOff
	}
	err := target - temp
	switch {
	case hasBoost && (err >= boostBelow || (prev == stageBoost && err > boostBelow-hyst)):
		return stageBoost
	case err >= hyst:
		return stageHeat
	case err <= -hyst:
		return stageOff
	case prev == stageBoost:
		return stageHeat
	default:
		return prev
	}
}

type thermoState struct {
	Enabled bool    `json:"enabled"`
	Target  float64 `json:"target"`
}

// controlled is the device side of the thermostat (implemented by *device).
type controlled interface {
	set(steps ...tuya.DPS)
	snapshot() (tuya.DPS, time.Time)
}

type thermostat struct {
	cfg   *ThermostatConfig
	dev   controlled
	store string // file for target/mode (survives bridge restarts)
	now   func() time.Time

	mu        sync.Mutex
	st        thermoState
	temp      float64
	tempAt    time.Time
	stage     stage
	listeners []func()
}

func newThermostat(cfg *ThermostatConfig, dev controlled, storageDir string) *thermostat {
	t := &thermostat{cfg: cfg, dev: dev, now: time.Now,
		store: filepath.Join(storageDir, "thermostat.json"),
		st:    thermoState{Enabled: true, Target: cfg.DefaultTarget}}
	if b, err := os.ReadFile(t.store); err == nil {
		var s thermoState
		if json.Unmarshal(b, &s) == nil && s.Target >= cfg.MinTarget && s.Target <= cfg.MaxTarget {
			t.st = s
		}
	}
	return t
}

func (t *thermostat) save() {
	b, _ := json.Marshal(t.st)
	if err := os.WriteFile(t.store, b, 0o600); err != nil {
		log.Printf("thermostat: saving state: %v", err)
	}
}

func (t *thermostat) onChange(fn func()) {
	t.mu.Lock()
	t.listeners = append(t.listeners, fn)
	t.mu.Unlock()
}

func (t *thermostat) notify() {
	t.mu.Lock()
	ls := t.listeners
	t.mu.Unlock()
	for _, fn := range ls {
		fn()
	}
}

// Snapshot is the externally visible state.
type thermoSnapshot struct {
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Target  float64  `json:"target"`
	Min     float64  `json:"min"`
	Max     float64  `json:"max"`
	Temp    *float64 `json:"temperature,omitempty"`
	TempAge *int     `json:"temperature_age_s,omitempty"`
	Fresh   bool     `json:"fresh"`
	Stage   string   `json:"stage"`
	Heating bool     `json:"heating"`
	MaxAgeS int      `json:"max_age_s"`
}

func (t *thermostat) snapshot() thermoSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := thermoSnapshot{Name: t.cfg.Name, Enabled: t.st.Enabled, Target: t.st.Target,
		Min: t.cfg.MinTarget, Max: t.cfg.MaxTarget, Stage: t.stage.String(),
		Heating: t.stage != stageOff, MaxAgeS: int(t.cfg.MaxAge.Seconds()), Fresh: t.freshLocked()}
	if !t.tempAt.IsZero() {
		v, age := t.temp, int(t.now().Sub(t.tempAt).Seconds())
		s.Temp, s.TempAge = &v, &age
	}
	return s
}

func (t *thermostat) freshLocked() bool {
	return !t.tempAt.IsZero() && t.now().Sub(t.tempAt) <= t.cfg.MaxAge.Duration
}

// SetTemperature records a new room temperature and re-evaluates.
func (t *thermostat) SetTemperature(v float64) {
	t.mu.Lock()
	t.temp, t.tempAt = v, t.now()
	t.mu.Unlock()
	t.evaluate("temperature")
}

// SetTarget changes the target temperature (clamped, rounded to 0.5 °C).
func (t *thermostat) SetTarget(v float64) {
	v = math.Round(v*2) / 2
	v = math.Max(t.cfg.MinTarget, math.Min(t.cfg.MaxTarget, v))
	t.mu.Lock()
	t.st.Target = v
	t.save()
	t.mu.Unlock()
	log.Printf("thermostat: target %.1f °C", v)
	t.evaluate("target")
}

// SetEnabled switches the thermostat between heat (true) and off (false).
// Turning it off switches the device off once; while off the device is not
// touched, so manual switches keep working.
func (t *thermostat) SetEnabled(on bool) {
	t.mu.Lock()
	t.st.Enabled = on
	t.save()
	t.mu.Unlock()
	log.Printf("thermostat: mode %s", map[bool]string{true: "heat", false: "off"}[on])
	t.evaluate("mode")
}

func (t *thermostat) stageDPS(s stage) Stage {
	switch s {
	case stageBoost:
		return *t.cfg.Boost
	case stageHeat:
		return t.cfg.Heat
	default:
		return t.cfg.Off
	}
}

// evaluate decides the stage and writes it to the device if the stage changed
// or the device drifted from it (e.g. someone used a switch) while enabled.
func (t *thermostat) evaluate(reason string) {
	t.mu.Lock()
	fresh := t.freshLocked()
	prev := t.stage
	next := decide(prev, t.st.Enabled, fresh, t.temp, t.st.Target, t.cfg.Hysteresis, t.cfg.BoostBelow, t.cfg.Boost != nil)
	enabled := t.st.Enabled
	t.stage = next
	temp, target := t.temp, t.st.Target
	t.mu.Unlock()

	want := t.stageDPS(next)
	dps, _ := t.dev.snapshot()
	inSync := dps != nil && matches(dps, want.Set) && matches(dps, want.Then)
	// Only enforce while enabled; when disabled, act on the transition only.
	if next != prev || (enabled && !inSync) || (reason == "mode" && !enabled) {
		why := reason
		if enabled && !fresh {
			why = "no recent temperature (failsafe)"
		}
		log.Printf("thermostat: %s -> %s (room %.1f °C, target %.1f °C, %s)", prev, next, temp, target, why)
		t.dev.set(want.Set, want.Then)
	}
	t.notify()
}

// run re-evaluates periodically so the failsafe triggers without new input.
func (t *thermostat) run(stop <-chan struct{}) {
	tk := time.NewTicker(time.Minute)
	defer tk.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tk.C:
			t.evaluate("periodic check")
		}
	}
}

// Fixed HomeKit accessory IDs, so adding or removing switch accessories never
// renumbers the thermostat (HomeKit identifies accessories by ID).
const (
	thermostatAID = 100
	triggerAID    = 101
)

// buildAccessories returns the HomeKit thermostat (and the trigger sensor).
func (t *thermostat) buildAccessories() []*accessory.A {
	info := accessory.Info{Name: t.cfg.Name, Manufacturer: "tuya-homekit-bridge", Model: "Thermostat"}
	th := accessory.NewThermostat(info)
	th.A.Id = thermostatAID
	s := th.Thermostat
	s.TargetTemperature.SetMinValue(t.cfg.MinTarget)
	s.TargetTemperature.SetMaxValue(t.cfg.MaxTarget)
	s.TargetTemperature.SetStepValue(0.5)
	s.CurrentTemperature.SetMinValue(-20)
	s.CurrentTemperature.SetMaxValue(60)
	s.TargetHeatingCoolingState.ValidVals = []int{characteristic.TargetHeatingCoolingStateOff, characteristic.TargetHeatingCoolingStateHeat}
	s.TemperatureDisplayUnits.SetValue(characteristic.TemperatureDisplayUnitsCelsius)

	s.TargetTemperature.OnValueRemoteUpdate(func(v float64) { t.SetTarget(v) })
	s.TargetHeatingCoolingState.OnValueRemoteUpdate(func(v int) {
		t.SetEnabled(v == characteristic.TargetHeatingCoolingStateHeat)
	})
	update := func() {
		snap := t.snapshot()
		s.TargetTemperature.SetValue(snap.Target)
		if snap.Temp != nil {
			s.CurrentTemperature.SetValue(*snap.Temp)
		}
		if snap.Enabled {
			s.TargetHeatingCoolingState.SetValue(characteristic.TargetHeatingCoolingStateHeat)
		} else {
			s.TargetHeatingCoolingState.SetValue(characteristic.TargetHeatingCoolingStateOff)
		}
		if snap.Heating {
			s.CurrentHeatingCoolingState.SetValue(characteristic.CurrentHeatingCoolingStateHeat)
		} else {
			s.CurrentHeatingCoolingState.SetValue(characteristic.CurrentHeatingCoolingStateOff)
		}
	}
	t.onChange(update)
	update()
	out := []*accessory.A{th.A}

	if tc := t.cfg.Trigger; tc != nil {
		m := accessory.NewMotionSensor(accessory.Info{Name: tc.Name, Manufacturer: "tuya-homekit-bridge", Model: "Trigger"})
		m.A.Id = triggerAID
		go func() {
			tk := time.NewTicker(tc.Interval.Duration)
			defer tk.Stop()
			for range tk.C {
				m.MotionSensor.MotionDetected.SetValue(true)
				time.Sleep(10 * time.Second)
				m.MotionSensor.MotionDetected.SetValue(false)
			}
		}()
		out = append(out, m.A)
	}
	return out
}

var numberRe = regexp.MustCompile(`-?\d+(?:[.,]\d+)?`)

// parseTemperature accepts "21.5", "21,5", "21,5 °C" etc.
func parseTemperature(s string) (float64, error) {
	m := numberRe.FindString(s)
	if m == "" {
		return 0, fmt.Errorf("no number in %q", s)
	}
	v, err := strconv.ParseFloat(strings.Replace(m, ",", ".", 1), 64)
	if err != nil || v < -40 || v > 80 {
		return 0, fmt.Errorf("implausible temperature %q", s)
	}
	if strings.Contains(s, "°F") {
		v = (v - 32) * 5 / 9
	}
	return v, nil
}

// handleTemperature serves GET/POST /api/temperature?token=...&value=21.5
// (value may also be sent as the request body). Token-authenticated so that a
// Home automation can call it without a browser session.
func (t *thermostat) handleTemperature(rw http.ResponseWriter, r *http.Request) {
	tok := r.Header.Get("X-Token")
	if tok == "" {
		tok = r.FormValue("token")
	}
	if subtle.ConstantTimeCompare([]byte(tok), []byte(t.cfg.Token)) != 1 {
		time.Sleep(time.Second)
		http.Error(rw, "unauthorized", http.StatusUnauthorized)
		return
	}
	raw := r.FormValue("value")
	if raw == "" && r.Method == http.MethodPost {
		buf := make([]byte, 256)
		n, _ := r.Body.Read(buf)
		raw = string(buf[:n])
	}
	v, err := parseTemperature(raw)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	t.SetTemperature(v)
	rw.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(rw, "ok %.1f\n", v)
}
