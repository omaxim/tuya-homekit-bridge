// hapkeys extracts ONLY the HomeKit pairing records (keychain access group
// "com.apple.hap.pairing") from an encrypted local iPhone backup.
//
// Everything is decrypted in memory. Other keychain items are decrypted one
// at a time only to read their access group and are then dropped; nothing
// but the HAP records is ever written. No network code is linked in.
//
// Built from a trimmed, reviewed copy of github.com/dunhamsteve/ios
// (MIT, see LICENSE.irestore) with the key/password debug prints removed.
//
// Usage: hapkeys [-backup <name or udid>] -out <file.json>
package main

import (
	"bytes"
	"crypto/aes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"

	"golang.org/x/term"

	"hapkeys/internal/aeswrap"
	"hapkeys/internal/backup"
	"hapkeys/internal/gcm"
	"hapkeys/internal/plist"
)

type kcEntry struct {
	Data []byte `plist:"v_Data"`
}

type keychain struct {
	General  []kcEntry `plist:"genp"`
	Internet []kcEntry `plist:"inet"`
	Certs    []kcEntry `plist:"cert"`
	Keys     []kcEntry `plist:"keys"`
}

type Controller struct {
	ID   string `json:"id"`
	LTPK string `json:"ltpk"`
	LTSK string `json:"ltsk"`
}

type Accessory struct {
	ID   string `json:"id"`
	LTPK string `json:"ltpk"`
}

type Out struct {
	Controllers []Controller `json:"controllers"`
	Accessories []Accessory  `json:"accessories"`
}

func die(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "hapkeys: "+f+"\n", a...)
	os.Exit(1)
}

func asBytes(v interface{}) []byte {
	switch x := v.(type) {
	case []byte:
		return x
	case string:
		return []byte(x)
	}
	return nil
}

func main() {
	sel := flag.String("backup", "", "device name or backup folder (default: the only backup)")
	out := flag.String("out", "", "output JSON file (required, written with mode 0600)")
	flag.Parse()
	if *out == "" {
		die("-out is required")
	}

	all, err := backup.Enumerate()
	if err != nil {
		die("cannot list backups (Terminal may need Full Disk Access): %v", err)
	}
	var chosen *backup.Backup
	for i, b := range all {
		if *sel == "" && len(all) == 1 || *sel != "" && (b.DeviceName == *sel || b.FileName == *sel) {
			chosen = &all[i]
		}
	}
	if chosen == nil {
		for _, b := range all {
			fmt.Fprintf(os.Stderr, "  %s\t%s\n", b.DeviceName, b.FileName)
		}
		die("choose a backup with -backup (found %d)", len(all))
	}
	fmt.Fprintf(os.Stderr, "Backup: %s (%s)\n", chosen.DeviceName, chosen.FileName)

	db, err := backup.Open(chosen.FileName)
	if err != nil {
		die("open: %v", err)
	}
	if !db.Manifest.IsEncrypted {
		die("backup is not encrypted - the keychain is only included in encrypted backups")
	}
	fmt.Fprint(os.Stderr, "Backup password (not shown): ")
	pw, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		die("read password: %v", err)
	}
	if err := db.SetPassword(string(pw)); err != nil {
		die("wrong password?")
	}
	for i := range pw {
		pw[i] = 0
	}
	if err := db.Load(); err != nil {
		die("load: %v", err)
	}

	var raw []byte
	for _, rec := range db.Records {
		if rec.Domain == "KeychainDomain" && rec.Path == "keychain-backup.plist" {
			if raw, err = db.ReadFile(rec); err != nil {
				die("read keychain: %v", err)
			}
		}
	}
	if raw == nil {
		die("no keychain in this backup")
	}
	var kc keychain
	if err := plist.Unmarshal(bytes.NewReader(raw), &kc); err != nil {
		die("keychain plist: %v", err)
	}

	// Diagnostics: counts only, never item names or contents.
	stats := map[string]int{}
	related := map[string]int{} // access groups containing "hap"/"home" (Apple group names only)
	fmt.Fprintf(os.Stderr, "Keybag class keys:")
	for _, k := range db.Keybag.Keys {
		fmt.Fprintf(os.Stderr, " %d(wrap %d,%v)", k.Class, k.Wrap, k.Key != nil)
	}
	fmt.Fprintln(os.Stderr)

	le := binary.LittleEndian
	var res Out
	skipped := 0
	groups := map[string][]kcEntry{"genp": kc.General, "inet": kc.Internet, "cert": kc.Certs, "keys": kc.Keys}
	for gname, items := range groups {
		for _, it := range items {
			d := it.Data
			stats[gname+" total"]++
			if len(d) < 12 {
				stats["too short"]++
				skipped++
				continue
			}
			if v := le.Uint32(d); v != 3 {
				stats[fmt.Sprintf("blob version %d", v)]++
				skipped++
				continue
			}
			class := le.Uint32(d[4:])
			l := le.Uint32(d[8:])
			if uint64(12+l) > uint64(len(d)) {
				stats["bad length"]++
				skipped++
				continue
			}
			ckey := db.Keybag.GetClassKey(class)
			if ckey == nil {
				stats[fmt.Sprintf("no key for class %d", class)]++
				skipped++ // device-only class, not decryptable from a backup
				continue
			}
			k := aeswrap.Unwrap(ckey, d[12:12+l])
			if k == nil {
				stats[fmt.Sprintf("unwrap failed class %d", class)]++
				skipped++
				continue
			}
			c, err := aes.NewCipher(k)
			if err != nil {
				stats["bad item key"]++
				continue
			}
			g, _ := gcm.NewGCM(c)
			plain, err := g.Open(nil, nil, d[12+l:], nil)
			if err != nil {
				stats[fmt.Sprintf("gcm failed class %d", class)]++
				skipped++
				continue
			}
			f, shape, err := parseItem(plain)
			if err != nil {
				stats["parse failed: "+shape]++
				continue
			}
			stats[fmt.Sprintf("decrypted class %d", class)]++
			agrp := string(asBytes(f["agrp"]))
			if la := strings.ToLower(agrp); strings.HasPrefix(la, "com.apple.") && (strings.Contains(la, "hap") || strings.Contains(la, "home")) {
				related[gname+" "+agrp]++
			}
			if agrp != "com.apple.hap.pairing" {
				continue // not HomeKit: dropped without being looked at further
			}
			if t, ok := f["tomb"].(int64); ok && t == 1 {
				continue
			}
			id := strings.TrimSpace(string(asBytes(f["acct"])))
			v := asBytes(f["v_Data"])
			svce := string(asBytes(f["svce"]))
			switch {
			case strings.HasPrefix(svce, "HomeKit Pairing Identity"):
				if c, err := parseIdentity(id, v); err == nil {
					res.addController(c)
				} else {
					fmt.Fprintf(os.Stderr, "  identity %s: %v (format %s)\n", id, err, shapeOf(v))
				}
			case strings.HasPrefix(svce, "Paired HomeKit Accessory:"):
				if a, err := parseAccessory(id, v); err == nil {
					res.Accessories = append(res.Accessories, a)
				} else {
					fmt.Fprintf(os.Stderr, "  accessory %s: %v (format %s)\n", id, err, shapeOf(v))
				}
			}
		}

	}
	fmt.Fprintln(os.Stderr, "Item stats:")
	for k, v := range stats {
		fmt.Fprintf(os.Stderr, "  %-28s %d\n", k, v)
	}
	fmt.Fprintln(os.Stderr, "Apple HomeKit-related access groups:")
	for k, v := range related {
		fmt.Fprintf(os.Stderr, "  %-40s %d\n", k, v)
	}

	js, _ := json.MarshalIndent(res, "", "  ")
	if err := os.WriteFile(*out, js, 0600); err != nil {
		die("write: %v", err)
	}
	fmt.Fprintf(os.Stderr, "HomeKit records: %d controller key(s), %d accessories (%d other items not decryptable from backup)\n",
		len(res.Controllers), len(res.Accessories), skipped)
	for _, c := range res.Controllers {
		fmt.Fprintf(os.Stderr, "  controller %s\n", c.ID)
	}
	for _, a := range res.Accessories {
		fmt.Fprintf(os.Stderr, "  accessory  %s\n", a.ID)
	}
	fmt.Fprintf(os.Stderr, "Written to %s\n", *out)
}
