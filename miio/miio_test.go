package miio

import (
	"bytes"
	"testing"
)

func TestEncryptRoundTrip(t *testing.T) {
	d := &Device{Token: "00112233445566778899aabbccddeeff"}
	if err := d.init(); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []string{"{}", `{"id":1,"method":"get_prop","params":["power"]}`, string(bytes.Repeat([]byte("x"), 16))} {
		enc, err := d.encrypt([]byte(msg))
		if err != nil {
			t.Fatal(err)
		}
		dec, err := d.decrypt(enc)
		if err != nil || string(dec) != msg {
			t.Fatalf("round trip %q -> %q (%v)", msg, dec, err)
		}
	}
	if err := (&Device{Token: "short"}).init(); err == nil {
		t.Fatal("expected error for bad token")
	}
}
