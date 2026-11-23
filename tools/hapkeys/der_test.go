package main

import (
	"crypto/ed25519"
	"encoding/asn1"
	"encoding/hex"
	"testing"
)

func TestParseItem(t *testing.T) {
	type kvS struct {
		K string `asn1:"utf8"`
		V string `asn1:"utf8"`
	}
	type kvB struct {
		K string `asn1:"utf8"`
		V []byte
	}
	type kvI struct {
		K string `asn1:"utf8"`
		V int
	}
	var body []byte
	for _, x := range []any{kvS{"agrp", "com.apple.hap.pairing"}, kvB{"v_Data", make([]byte, 64)}, kvI{"tomb", 0}} {
		b, _ := asn1.Marshal(x)
		body = append(body, b...)
	}
	set, _ := asn1.Marshal(asn1.RawValue{Class: 0, Tag: asn1.TagSet, IsCompound: true, Bytes: body})
	f, _, err := parseItem(set)
	if err != nil || string(asBytes(f["agrp"])) != "com.apple.hap.pairing" || len(asBytes(f["v_Data"])) != 64 || f["tomb"] != int64(0) {
		t.Fatalf("%v %v", err, f)
	}
}

func TestParseIdentity(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	v := []byte(hex.EncodeToString(pub) + "+" + hex.EncodeToString(priv.Seed()))
	if len(v) != 129 {
		t.Fatal(len(v))
	}
	c, err := parseIdentity("X", v)
	if err != nil || c.LTSK != hex.EncodeToString(priv.Seed()) {
		t.Fatal(err)
	}
	a, err := parseAccessory("A", []byte(hex.EncodeToString(pub)))
	if err != nil || a.LTPK != hex.EncodeToString(pub) {
		t.Fatal(err)
	}
}
