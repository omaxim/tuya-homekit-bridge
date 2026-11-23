package miio

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Yeelight wraps a miIO device that speaks the Yeelight light commands
// (yeelink.light.* models paired through Mi Home).
type Yeelight struct {
	*Device
	Smooth int // transition in ms (default 300)
}

// LightState is the current state of a Yeelight bulb.
type LightState struct {
	On         bool
	Brightness int // 1..100
	CT         int // colour temperature in kelvin
	Hue        int // 0..359
	Sat        int // 0..100
	ColorMode  int // 1 = RGB, 2 = colour temperature, 3 = HSV
}

var props = []string{"power", "bright", "ct", "hue", "sat", "color_mode"}

// Status reads the bulb state.
func (y *Yeelight) Status() (LightState, error) {
	raw, err := y.Send("get_prop", props)
	if err != nil {
		return LightState{}, err
	}
	var vals []string
	if err := json.Unmarshal(raw, &vals); err != nil || len(vals) < len(props) {
		return LightState{}, fmt.Errorf("miio: unexpected get_prop reply %s", raw)
	}
	n := func(s string) int { v, _ := strconv.Atoi(s); return v }
	return LightState{On: vals[0] == "on", Brightness: n(vals[1]), CT: n(vals[2]),
		Hue: n(vals[3]), Sat: n(vals[4]), ColorMode: n(vals[5])}, nil
}

func (y *Yeelight) smooth() int {
	if y.Smooth > 0 {
		return y.Smooth
	}
	return 300
}

func (y *Yeelight) ok(method string, params []any) error {
	raw, err := y.Send(method, params)
	if err != nil {
		return err
	}
	var res []string
	if json.Unmarshal(raw, &res) == nil && len(res) > 0 && res[0] != "ok" {
		return fmt.Errorf("miio: %s returned %v", method, res)
	}
	return nil
}

// SetPower switches the light on or off.
func (y *Yeelight) SetPower(on bool) error {
	return y.ok("set_power", []any{map[bool]string{true: "on", false: "off"}[on], "smooth", y.smooth()})
}

// SetBrightness sets 1..100 % (the light must be on).
func (y *Yeelight) SetBrightness(b int) error {
	b = clamp(b, 1, 100)
	return y.ok("set_bright", []any{b, "smooth", y.smooth()})
}

// SetCT sets the colour temperature in kelvin (1700..6500).
func (y *Yeelight) SetCT(k int) error {
	return y.ok("set_ct_abx", []any{clamp(k, 1700, 6500), "smooth", y.smooth()})
}

// SetHSV sets hue (0..359) and saturation (0..100).
func (y *Yeelight) SetHSV(h, s int) error {
	return y.ok("set_hsv", []any{clamp(h, 0, 359), clamp(s, 0, 100), "smooth", y.smooth()})
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
