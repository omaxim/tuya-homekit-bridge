package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/omaxim/tuya-homekit-bridge/tuya"
)

func TestDecide(t *testing.T) {
	const target, hyst, boost = 21.0, 0.3, 1.0
	cases := []struct {
		name    string
		prev    stage
		enabled bool
		fresh   bool
		temp    float64
		want    stage
	}{
		{"cold -> boost", stageOff, true, true, 19.5, stageBoost},
		{"slightly cold -> heat", stageOff, true, true, 20.5, stageHeat},
		{"boost kept until close", stageBoost, true, true, 20.2, stageBoost},
		{"boost drops to heat", stageBoost, true, true, 20.4, stageHeat},
		{"dead band keeps heat", stageHeat, true, true, 21.1, stageHeat},
		{"dead band keeps off", stageOff, true, true, 20.9, stageOff},
		{"warm -> off", stageHeat, true, true, 21.3, stageOff},
		{"boost never stays in dead band", stageBoost, true, true, 21.0, stageHeat},
		{"disabled -> off", stageHeat, false, true, 15, stageOff},
		{"stale temperature -> off (failsafe)", stageHeat, true, false, 15, stageOff},
	}
	for _, c := range cases {
		if got := decide(c.prev, c.enabled, c.fresh, c.temp, target, hyst, boost, true); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
	if got := decide(stageOff, true, true, 15, target, hyst, boost, false); got != stageHeat {
		t.Errorf("without boost stage: got %s, want heat", got)
	}
}

func TestParseTemperature(t *testing.T) {
	ok := map[string]float64{"21.5": 21.5, "21,5": 21.5, "21,5 °C": 21.5, " 19 °C": 19, "-3,2": -3.2, "68 °F": 20}
	for in, want := range ok {
		got, err := parseTemperature(in)
		if err != nil || got < want-0.01 || got > want+0.01 {
			t.Errorf("parseTemperature(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "500"} {
		if _, err := parseTemperature(in); err == nil {
			t.Errorf("parseTemperature(%q): expected error", in)
		}
	}
}

// fakeDevice records writes and mimics device state.
type fakeDevice struct {
	dps    tuya.DPS
	writes []tuya.DPS
}

func (f *fakeDevice) set(steps ...tuya.DPS) {
	for _, s := range steps {
		if len(s) == 0 {
			continue
		}
		f.writes = append(f.writes, s)
		for k, v := range s {
			f.dps[k] = v
		}
	}
}
func (f *fakeDevice) snapshot() (tuya.DPS, time.Time) { return f.dps, time.Now() }

func heaterThermo(t *testing.T) (*thermostat, *fakeDevice, *time.Time) {
	cfg := &Config{Devices: []Device{{}}}
	tc := &ThermostatConfig{Token: "0123456789abcdef",
		Off:   Stage{Set: tuya.DPS{"1": false}},
		Heat:  Stage{Set: tuya.DPS{"2": float64(50), "1": true}, Then: tuya.DPS{"4": "low"}},
		Boost: &Stage{Set: tuya.DPS{"2": float64(50), "1": true}, Then: tuya.DPS{"4": "high"}}}
	if err := tc.setDefaults(cfg); err != nil {
		t.Fatal(err)
	}
	dev := &fakeDevice{dps: tuya.DPS{"1": false, "2": float64(50), "4": "low"}}
	th := newThermostat(tc, dev, t.TempDir())
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	th.now = func() time.Time { return now }
	return th, dev, &now
}

func TestThermostatScenario(t *testing.T) {
	th, dev, now := heaterThermo(t)

	th.SetTemperature(18) // cold room
	if dev.dps["1"] != true || dev.dps["4"] != "high" {
		t.Fatalf("cold room: want boost, device %v", dev.dps)
	}
	th.SetTemperature(20.5) // boost until within 0.7 °C
	if dev.dps["4"] != "low" {
		t.Fatalf("near target: want low, device %v", dev.dps)
	}
	th.SetTemperature(21.4)
	if dev.dps["1"] != false {
		t.Fatalf("warm: want off, device %v", dev.dps)
	}
	n := len(dev.writes)
	th.SetTemperature(21.2) // dead band: no writes
	if len(dev.writes) != n {
		t.Fatalf("dead band should not write, got %v", dev.writes[n:])
	}

	// Manual override while enabled is corrected on the next evaluation.
	dev.dps["1"] = true
	th.SetTemperature(21.2)
	if dev.dps["1"] != false {
		t.Fatalf("drift not corrected: %v", dev.dps)
	}

	// Failsafe: no temperature for longer than max_age turns heating off.
	th.SetTemperature(19)
	if dev.dps["1"] != true {
		t.Fatalf("want heating")
	}
	*now = now.Add(21 * time.Minute)
	th.evaluate("periodic check")
	if dev.dps["1"] != false {
		t.Fatalf("failsafe did not turn off: %v", dev.dps)
	}

	// Disabled: turns off once, then leaves manual control alone.
	*now = now.Add(time.Minute)
	th.SetTemperature(19)
	th.SetEnabled(false)
	if dev.dps["1"] != false {
		t.Fatalf("disable should turn off")
	}
	dev.dps["1"] = true // someone uses the manual switch
	th.SetTemperature(19)
	if dev.dps["1"] != true {
		t.Fatalf("disabled thermostat must not override manual control")
	}
}

func TestTargetClampAndPersist(t *testing.T) {
	th, _, _ := heaterThermo(t)
	th.SetTarget(35)
	if s := th.snapshot(); s.Target != 28 {
		t.Fatalf("clamp: %v", s.Target)
	}
	th.SetTarget(20.26)
	reloaded := newThermostat(th.cfg, &fakeDevice{dps: tuya.DPS{}}, strings.TrimSuffix(th.store, "/thermostat.json"))
	if s := reloaded.snapshot(); s.Target != 20.5 {
		t.Fatalf("persisted target: %v", s.Target)
	}
}

func TestTemperatureEndpoint(t *testing.T) {
	th, dev, _ := heaterThermo(t)
	do := func(url string) int {
		rec := httptest.NewRecorder()
		th.handleTemperature(rec, httptest.NewRequest(http.MethodGet, url, nil))
		return rec.Code
	}
	if c := do("/api/temperature?token=wrong&value=18"); c != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", c)
	}
	if c := do("/api/temperature?token=0123456789abcdef&value=18,5%20%C2%B0C"); c != http.StatusOK {
		t.Fatalf("valid: %d", c)
	}
	if s := th.snapshot(); s.Temp == nil || *s.Temp != 18.5 || dev.dps["1"] != true {
		t.Fatalf("temperature not applied: %+v %v", s, dev.dps)
	}
	if c := do("/api/temperature?token=0123456789abcdef&value=hot"); c != http.StatusBadRequest {
		t.Fatalf("garbage: %d", c)
	}
}
