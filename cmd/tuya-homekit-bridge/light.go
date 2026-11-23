package main

// Lights: Xiaomi / Yeelight bulbs paired through Mi Home, controlled locally
// over the miIO protocol (needs the device token).

import (
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/omaxim/tuya-homekit-bridge/miio"
)

// LightConfig configures a Yeelight/Mi bulb.
type LightConfig struct {
	Name  string `json:"name"`
	Type  string `json:"type"` // "yeelight" (miIO)
	IP    string `json:"ip"`
	Token string `json:"token"` // 32 hex characters, from the Mi Home cloud
	Color bool   `json:"color"` // expose hue/saturation (RGB bulbs)
}

func (l *LightConfig) validate() error {
	if l.Type == "" {
		l.Type = "yeelight"
	}
	if l.Type != "yeelight" {
		return fmt.Errorf("light %q: unknown type %q", l.Name, l.Type)
	}
	if l.Name == "" || l.IP == "" || len(l.Token) != 32 {
		return errors.New("light: name, ip and a 32-character token are required")
	}
	return nil
}

// HomeKit accessory IDs for lights start here (fixed, see thermostatAID).
const lightAIDBase = 200

type light struct {
	cfg  LightConfig
	bulb *miio.Yeelight

	mu        sync.Mutex
	st        miio.LightState
	online    bool
	at        time.Time
	listeners []func()
}

func newLight(c LightConfig) *light {
	return &light{cfg: c, bulb: &miio.Yeelight{Device: &miio.Device{Addr: c.IP, Token: c.Token, Timeout: 4 * time.Second}}}
}

func (l *light) onChange(fn func()) {
	l.mu.Lock()
	l.listeners = append(l.listeners, fn)
	l.mu.Unlock()
}

func (l *light) notify() {
	l.mu.Lock()
	ls := l.listeners
	l.mu.Unlock()
	for _, fn := range ls {
		fn()
	}
}

func (l *light) snapshot() (miio.LightState, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st, l.online
}

func (l *light) refresh() {
	st, err := l.bulb.Status()
	l.mu.Lock()
	changed := err == nil && (st != l.st || !l.online) || err != nil && l.online
	if err == nil {
		l.st, l.online, l.at = st, true, time.Now()
	} else if time.Since(l.at) > 2*time.Minute {
		l.online = false
	}
	l.mu.Unlock()
	if err != nil {
		log.Printf("%s: status failed: %v", l.cfg.Name, err)
	}
	if changed {
		l.notify()
	}
}

func (l *light) poll(stop <-chan struct{}, every time.Duration) {
	l.refresh()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			l.refresh()
		}
	}
}

// apply runs a change, logs it and refreshes the state.
func (l *light) apply(what string, fn func() error) error {
	err := fn()
	if err != nil {
		log.Printf("%s: %s failed: %v", l.cfg.Name, what, err)
	} else {
		log.Printf("%s: %s", l.cfg.Name, what)
	}
	l.refresh()
	return err
}

func (l *light) SetOn(on bool) error {
	return l.apply(fmt.Sprintf("power %v", on), func() error { return l.bulb.SetPower(on) })
}

// ensureOn turns the light on first; Yeelights reject other changes while off.
func (l *light) ensureOn() error {
	if st, _ := l.snapshot(); !st.On {
		return l.bulb.SetPower(true)
	}
	return nil
}

func (l *light) SetBrightness(b int) error {
	return l.apply(fmt.Sprintf("brightness %d%%", b), func() error {
		if err := l.ensureOn(); err != nil {
			return err
		}
		return l.bulb.SetBrightness(b)
	})
}

func (l *light) SetCT(kelvin int) error {
	return l.apply(fmt.Sprintf("colour temperature %dK", kelvin), func() error {
		if err := l.ensureOn(); err != nil {
			return err
		}
		return l.bulb.SetCT(kelvin)
	})
}

func (l *light) SetHSV(h, s int) error {
	return l.apply(fmt.Sprintf("colour h=%d s=%d", h, s), func() error {
		if err := l.ensureOn(); err != nil {
			return err
		}
		return l.bulb.SetHSV(h, s)
	})
}

func kelvinToMired(k int) int { return int(math.Round(1e6 / float64(max(k, 1)))) }
func miredToKelvin(m int) int { return int(math.Round(1e6 / float64(max(m, 1)))) }

func (l *light) buildAccessory(aid uint64) *accessory.A {
	info := accessory.Info{Name: l.cfg.Name, Manufacturer: "Xiaomi", Model: "Yeelight"}
	a := accessory.NewColoredLightbulb(info)
	a.A.Id = aid
	s := a.Lightbulb
	ct := characteristic.NewColorTemperature()
	ct.SetMinValue(154) // 6500 K
	ct.SetMaxValue(500) // 2000 K
	s.AddC(ct.C)

	s.On.OnValueRemoteUpdate(func(on bool) { l.SetOn(on) })
	s.Brightness.OnValueRemoteUpdate(func(b int) { l.SetBrightness(b) })
	ct.OnValueRemoteUpdate(func(m int) { l.SetCT(miredToKelvin(m)) })
	// Hue and saturation arrive as two separate writes; combine them briefly.
	var hsMu sync.Mutex
	var pending *time.Timer
	setHS := func() {
		hsMu.Lock()
		if pending != nil {
			pending.Stop()
		}
		pending = time.AfterFunc(150*time.Millisecond, func() {
			l.SetHSV(int(s.Hue.Value()), int(s.Saturation.Value()))
		})
		hsMu.Unlock()
	}
	s.Hue.OnValueRemoteUpdate(func(float64) { setHS() })
	s.Saturation.OnValueRemoteUpdate(func(float64) { setHS() })

	update := func() {
		st, _ := l.snapshot()
		s.On.SetValue(st.On)
		if st.Brightness > 0 {
			s.Brightness.SetValue(st.Brightness)
		}
		if st.CT > 0 {
			ct.SetValue(min(max(kelvinToMired(st.CT), 154), 500))
		}
		s.Hue.SetValue(float64(st.Hue))
		s.Saturation.SetValue(float64(st.Sat))
	}
	l.onChange(update)
	return a.A
}

// lightJSON is the web UI view of a light.
type lightJSON struct {
	Index      int    `json:"index"`
	Name       string `json:"name"`
	Online     bool   `json:"online"`
	On         bool   `json:"on"`
	Brightness int    `json:"brightness"`
	CT         int    `json:"ct"`
	Hue        int    `json:"hue"`
	Sat        int    `json:"sat"`
	Mode       string `json:"mode"` // "ct" or "color"
	Color      bool   `json:"color"`
}

func (l *light) json(i int) lightJSON {
	st, online := l.snapshot()
	mode := "ct"
	if st.ColorMode == 1 || st.ColorMode == 3 {
		mode = "color"
	}
	return lightJSON{Index: i, Name: l.cfg.Name, Online: online, On: st.On, Brightness: st.Brightness,
		CT: st.CT, Hue: st.Hue, Sat: st.Sat, Mode: mode, Color: l.cfg.Color}
}
