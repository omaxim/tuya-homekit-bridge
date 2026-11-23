package main

import (
	stdasn1 "encoding/asn1"
	"errors"
	"fmt"
)

// parseItem decodes a keychain item: a DER SET of SEQUENCE { key string, value }.
// Values are returned as raw bytes (strings/octets), int64 (integers) or the
// raw element for anything else. shape describes the outer structure (tags
// only, no content) for diagnostics when the layout is unexpected.
func parseItem(b []byte) (map[string]interface{}, string, error) {
	var top stdasn1.RawValue
	if _, err := stdasn1.Unmarshal(b, &top); err != nil {
		return nil, fmt.Sprintf("top %#x", firstByte(b)), err
	}
	shape := fmt.Sprintf("top class %d tag %d", top.Class, top.Tag)
	if top.Tag != stdasn1.TagSet && top.Tag != stdasn1.TagSequence || !top.IsCompound {
		return nil, shape, errors.New("not a set")
	}
	out := map[string]interface{}{}
	rest := top.Bytes
	for len(rest) > 0 {
		var pair stdasn1.RawValue
		var err error
		if rest, err = stdasn1.Unmarshal(rest, &pair); err != nil {
			return nil, shape + " bad pair", err
		}
		var k, v stdasn1.RawValue
		r2, err := stdasn1.Unmarshal(pair.Bytes, &k)
		if err != nil {
			return nil, shape + fmt.Sprintf(" pair tag %d bad key", pair.Tag), err
		}
		if len(r2) == 0 {
			continue
		}
		if _, err := stdasn1.Unmarshal(r2, &v); err != nil {
			return nil, shape + fmt.Sprintf(" key tag %d bad value", k.Tag), err
		}
		key := string(k.Bytes)
		switch {
		case v.Class == 0 && v.Tag == stdasn1.TagInteger:
			var n int64
			for _, c := range v.Bytes {
				n = n<<8 | int64(c)
			}
			out[key] = n
		case v.Class == 0 && !v.IsCompound:
			out[key] = v.Bytes
		default:
			out[key] = v.FullBytes
		}
	}
	return out, shape, nil
}

func firstByte(b []byte) byte {
	if len(b) == 0 {
		return 0
	}
	return b[0]
}
