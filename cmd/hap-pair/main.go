// hap-pair adds a dedicated, non-admin controller pairing to a HomeKit
// accessory that is already in your Home, using your Home's existing admin
// key (extracted once from an encrypted iPhone backup). The bridge then uses
// only the new, limited key; the admin key is not needed afterwards.
//
//	hap-pair -keys hap-keys.json -id AB:CD:EF:01:23:45 [-addr host:port] [-out sensor.json]
//
// See docs/homepod-sensor.md.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/omaxim/tuya-homekit-bridge/hapclient"
)

type keyFile struct {
	Controllers []struct {
		ID   string `json:"id"`
		LTSK string `json:"ltsk"`
	} `json:"controllers"`
	Accessories []struct {
		ID   string `json:"id"`
		LTPK string `json:"ltpk"`
	} `json:"accessories"`
}

// Sensor is the result, in the format of the bridge's "homekit_sensor" config.
type Sensor struct {
	AccessoryID    string `json:"accessory_id"`
	AccessoryLTPK  string `json:"accessory_ltpk"`
	ControllerID   string `json:"controller_id"`
	ControllerSeed string `json:"controller_seed"`
}

func die(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "hap-pair: "+f+"\n", a...)
	os.Exit(1)
}

func uuid() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]))
}

func main() {
	keys := flag.String("keys", "", "JSON with controllers[{id,ltsk}] and accessories[{id,ltpk}]")
	id := flag.String("id", "", "accessory pairing ID (the mDNS \"id\" TXT value)")
	addr := flag.String("addr", "", "accessory host:port (default: find via mDNS)")
	out := flag.String("out", "homekit-sensor.json", "where to write the new limited credentials (mode 0600)")
	flag.Parse()
	if *keys == "" || *id == "" {
		flag.Usage()
		os.Exit(2)
	}

	raw, err := os.ReadFile(*keys)
	if err != nil {
		die("%v", err)
	}
	var kf keyFile
	if err := json.Unmarshal(raw, &kf); err != nil {
		die("keys: %v", err)
	}
	var acc hapclient.Accessory
	for _, a := range kf.Accessories {
		if strings.EqualFold(a.ID, *id) {
			ltpk, err := hex.DecodeString(a.LTPK)
			if err != nil || len(ltpk) != 32 {
				die("bad accessory key for %s", a.ID)
			}
			acc = hapclient.Accessory{ID: a.ID, LTPK: ltpk}
		}
	}
	if acc.ID == "" {
		die("accessory %s is not in %s", *id, *keys)
	}
	acc.Addr = *addr
	if acc.Addr == "" {
		if acc.Addr, err = hapclient.Discover(context.Background(), acc.ID, 10*time.Second); err != nil {
			die("%v", err)
		}
	}
	fmt.Printf("Accessory %s at %s\n", acc.ID, acc.Addr)

	// Find the admin controller key that this accessory accepts.
	var sess *hapclient.Session
	for _, c := range kf.Controllers {
		seed, err := hex.DecodeString(c.LTSK)
		if err != nil || len(seed) != 32 {
			continue
		}
		if s, err := hapclient.Dial(acc, hapclient.Controller{ID: c.ID, Seed: seed}); err == nil {
			fmt.Printf("Verified with existing controller %s\n", c.ID)
			sess = s
			break
		}
	}
	if sess == nil {
		die("none of the %d controller keys is accepted by the accessory", len(kf.Controllers))
	}
	doc, err := sess.Accessories()
	if err != nil {
		die("accessories: %v", err)
	}
	tempID, err := hapclient.FindCharacteristic(doc, "11")
	if err != nil {
		die("%v", err)
	}
	if v, err := sess.Read(tempID); err == nil && len(v) == 1 {
		fmt.Printf("Temperature now: %s °C (characteristic %s)\n", v[0].Value, tempID)
	}

	// New limited controller.
	seed := make([]byte, 32)
	rand.Read(seed)
	ctrl := hapclient.Controller{ID: uuid(), Seed: seed}
	if err := sess.AddPairing(ctrl.ID, ctrl.PublicKey(), hapclient.PermissionUser); err != nil {
		die("add pairing: %v", err)
	}
	if ps, err := sess.ListPairings(); err == nil {
		fmt.Printf("Pairings on the accessory now:\n")
		for _, p := range ps {
			fmt.Printf("  %s permission=%d\n", p.ID, p.Permission)
		}
	}
	sess.Close()

	u, err := hapclient.Dial(acc, ctrl)
	if err != nil {
		die("new pairing does not verify: %v", err)
	}
	v, err := u.Read(tempID)
	u.Close()
	if err != nil || len(v) != 1 {
		die("read with new pairing failed: %v", err)
	}
	fmt.Printf("New controller %s works, temperature %s °C\n", ctrl.ID, v[0].Value)

	js, _ := json.MarshalIndent(Sensor{acc.ID, hex.EncodeToString(acc.LTPK), ctrl.ID, hex.EncodeToString(seed)}, "", "  ")
	if err := os.WriteFile(*out, append(js, '\n'), 0600); err != nil {
		die("%v", err)
	}
	fmt.Printf("Saved to %s - you can now delete the keys file and the backup.\n", *out)
}
