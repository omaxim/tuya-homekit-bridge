// Command tuya-homekit-bridge exposes Tuya Wi-Fi devices (local protocol 3.5)
// as Apple HomeKit accessories, with no cloud dependency at runtime.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
	"github.com/omaxim/tuya-homekit-bridge/tuya"
)

var version = "dev" // set at build time

func main() {
	cfgPath := flag.String("config", "config.json", "path to the configuration file")
	probe := flag.Bool("probe", false, "print the current data points of every configured device and exit")
	discover := flag.Duration("discover", 0, "listen for Tuya devices on the LAN for the given time (e.g. 10s) and exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	switch {
	case *showVersion:
		fmt.Println(version)
		return
	case *discover > 0:
		runDiscover(*discover)
		return
	}

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	devs := make([]*device, len(cfg.Devices))
	for i := range cfg.Devices {
		devs[i] = newDevice(cfg.Devices[i])
	}

	if *probe {
		for _, d := range devs {
			dps, err := d.status()
			fmt.Printf("%s: %v (error: %v)\n", d.cfg.Name, dps, err)
		}
		return
	}

	bridge := accessory.NewBridge(accessory.Info{Name: cfg.Name, Manufacturer: "tuya-homekit-bridge", Model: version})
	var accs []*accessory.A
	for _, d := range devs {
		accs = append(accs, d.buildAccessories()...)
	}

	server, err := hap.NewServer(hap.NewFsStore(cfg.Storage), bridge.A, accs...)
	if err != nil {
		log.Fatal(err)
	}
	server.Pin = cfg.Pin
	server.Ifaces = cfg.Interfaces

	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; cancel() }()

	for _, d := range devs {
		go d.poll(ctx, cfg.PollInterval.Duration)
	}
	startWeb(cfg, devs)

	log.Printf("tuya-homekit-bridge %s: %q with %d accessories, setup code %s-%s-%s",
		version, cfg.Name, len(accs), cfg.Pin[:3], cfg.Pin[3:5], cfg.Pin[5:])
	if err := server.ListenAndServe(ctx); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

func runDiscover(d time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	seen := map[string]bool{}
	fmt.Println("Searching for Tuya protocol 3.5 devices...")
	err := tuya.Discover(ctx, func(a tuya.Announcement) {
		if !seen[a.ID] {
			seen[a.ID] = true
			fmt.Printf("id=%s ip=%s product=%s version=%s\n", a.ID, a.IP, a.ProductID, a.Version)
		}
	})
	if err != nil {
		log.Fatal(err)
	}
	if len(seen) == 0 {
		fmt.Println("No devices found. Note: only protocol 3.5 broadcasts are decoded.")
	}
}

// device is the runtime state of one configured Tuya device.
type device struct {
	cfg Device
	dev *tuya.Device

	mu       sync.Mutex
	updaters []func(tuya.DPS)
	last     tuya.DPS
	lastAt   time.Time
}

// snapshot returns the last known data points and when they were read.
func (d *device) snapshot() (tuya.DPS, time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.last, d.lastAt
}

func newDevice(c Device) *device {
	return &device{cfg: c, dev: &tuya.Device{ID: c.ID, Addr: c.IP, LocalKey: c.LocalKey}}
}

// ensureAddr discovers the device IP if none is configured or the last one failed.
func (d *device) ensureAddr(force bool) error {
	if d.dev.Addr != "" && !force {
		return nil
	}
	ip, err := tuya.FindIP(d.cfg.ID, 15*time.Second)
	if err != nil {
		return fmt.Errorf("%s: device not found on the LAN: %w", d.cfg.Name, err)
	}
	if ip != d.dev.Addr {
		log.Printf("%s: found at %s", d.cfg.Name, ip)
	}
	d.dev.Addr = ip
	return nil
}

func (d *device) status() (tuya.DPS, error) {
	if err := d.ensureAddr(false); err != nil {
		return nil, err
	}
	dps, err := d.dev.Status()
	if err != nil && d.cfg.IP == "" {
		// The device may have moved to a new address; rediscover once.
		if d.ensureAddr(true) == nil {
			dps, err = d.dev.Status()
		}
	}
	return dps, err
}

// set writes one or more DP groups in order, then refreshes state.
func (d *device) set(steps ...tuya.DPS) {
	for _, v := range steps {
		if len(v) == 0 {
			continue
		}
		if err := d.dev.Set(v); err != nil {
			log.Printf("%s: set %v failed: %v", d.cfg.Name, v, err)
			break
		}
		log.Printf("%s: set %v", d.cfg.Name, v)
	}
	d.refresh()
}

func (d *device) refresh() {
	dps, err := d.status()
	if err != nil {
		log.Printf("%s: status failed: %v", d.cfg.Name, err)
		return
	}
	d.mu.Lock()
	ups := d.updaters
	d.last, d.lastAt = dps, time.Now()
	d.mu.Unlock()
	for _, u := range ups {
		u(dps)
	}
	for _, r := range d.cfg.Enforce {
		if matches(dps, r.When) && !matches(dps, r.Set) {
			log.Printf("%s: enforcing %v", d.cfg.Name, r.Set)
			if err := d.dev.Set(r.Set); err != nil {
				log.Printf("%s: enforce failed: %v", d.cfg.Name, err)
			}
		}
	}
}

func (d *device) poll(ctx context.Context, every time.Duration) {
	d.refresh()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.refresh()
		}
	}
}

func (d *device) onUpdate(fn func(tuya.DPS)) {
	d.mu.Lock()
	d.updaters = append(d.updaters, fn)
	d.mu.Unlock()
}

func (d *device) buildAccessories() []*accessory.A {
	var out []*accessory.A
	for _, a := range d.cfg.Accessories {
		info := accessory.Info{Name: a.Name, Manufacturer: "Tuya", Model: d.cfg.Name, SerialNumber: d.cfg.ID}
		switch a.Type {
		case "switch":
			sw := accessory.NewSwitch(info)
			a := a
			sw.Switch.On.OnValueRemoteUpdate(func(on bool) {
				if on {
					d.set(a.On, a.OnThen)
				} else {
					d.set(a.Off, a.OffThen)
				}
			})
			d.onUpdate(func(dps tuya.DPS) { sw.Switch.On.SetValue(matches(dps, a.State)) })
			out = append(out, sw.A)
		case "temperature":
			th := accessory.NewTemperatureSensor(info)
			a := a
			th.TempSensor.CurrentTemperature.SetMinValue(-40)
			th.TempSensor.CurrentTemperature.SetMaxValue(100)
			d.onUpdate(func(dps tuya.DPS) {
				if v, ok := dps[a.DP].(float64); ok {
					if a.Scale > 0 {
						v /= a.Scale
					}
					th.TempSensor.CurrentTemperature.SetValue(v)
				}
			})
			out = append(out, th.A)
		}
	}
	return out
}

// matches reports whether every DP in want has the same value in have.
func matches(have, want tuya.DPS) bool {
	for k, v := range want {
		if fmt.Sprint(have[k]) != fmt.Sprint(v) {
			return false
		}
	}
	return true
}
