package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// parseIdentity reads an iOS "HomeKit Pairing Identity": "<LTPK hex>+<LTSK hex>".
func parseIdentity(id string, v []byte) (Controller, error) {
	parts := strings.Split(strings.TrimSpace(string(v)), "+")
	if len(parts) != 2 {
		return Controller{}, errors.New("not pub+sec")
	}
	pub, err1 := hex.DecodeString(parts[0])
	sec, err2 := hex.DecodeString(parts[1])
	if err1 != nil || err2 != nil || len(pub) != 32 || (len(sec) != 32 && len(sec) != 64) {
		return Controller{}, errors.New("unexpected key lengths")
	}
	seed := sec[:32]
	if !bytes.Equal(ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey), pub) {
		return Controller{}, errors.New("secret does not match public key")
	}
	return Controller{ID: id, LTPK: hex.EncodeToString(pub), LTSK: hex.EncodeToString(seed)}, nil
}

// parseAccessory reads a "Paired HomeKit Accessory" record: the LTPK in hex (or raw).
func parseAccessory(id string, v []byte) (Accessory, error) {
	t := strings.TrimSpace(string(v))
	if b, err := hex.DecodeString(t); err == nil && len(b) == 32 {
		return Accessory{ID: id, LTPK: t}, nil
	}
	if len(v) == 32 {
		return Accessory{ID: id, LTPK: hex.EncodeToString(v)}, nil
	}
	return Accessory{}, errors.New("unexpected format")
}

func (o *Out) addController(c Controller) {
	for _, x := range o.Controllers {
		if x.ID == c.ID && x.LTSK == c.LTSK {
			return
		}
	}
	o.Controllers = append(o.Controllers, c)
}

// shapeOf describes data without revealing it: length and character classes.
func shapeOf(v []byte) string {
	var hexc, other, plus int
	for _, c := range v {
		switch {
		case c == '+':
			plus++
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
			hexc++
		default:
			other++
		}
	}
	return fmt.Sprintf("%d bytes: %d hex chars, %d '+', %d other", len(v), hexc, plus, other)
}
