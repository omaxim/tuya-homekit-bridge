package hapclient

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/brutella/dnssd"
)

// Discover finds the current address (host:port) of a HAP accessory by its
// pairing ID (the "id" TXT record of its _hap._tcp service). Accessories such
// as the HomePod pick a new port after a restart, so look it up again when a
// connection fails.
func Discover(ctx context.Context, id string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	found := make(chan string, 1)
	add := func(e dnssd.BrowseEntry) {
		if !strings.EqualFold(e.Text["id"], id) || e.Port == 0 {
			return
		}
		for _, ip := range e.IPs {
			if ip.To4() != nil {
				select {
				case found <- net.JoinHostPort(ip.String(), strconv.Itoa(e.Port)):
				default:
				}
				return
			}
		}
	}
	go dnssd.LookupType(ctx, "_hap._tcp.local.", add, func(dnssd.BrowseEntry) {})
	select {
	case a := <-found:
		return a, nil
	case <-ctx.Done():
		return "", errors.New("hapclient: accessory " + id + " not found via mDNS")
	}
}

// FindCharacteristic returns "aid.iid" of the first characteristic of the
// given short HAP type (e.g. "11" for CurrentTemperature) in an /accessories
// document.
func FindCharacteristic(accessories []byte, typ string) (string, error) {
	var doc struct {
		Accessories []struct {
			AID      uint64 `json:"aid"`
			Services []struct {
				Characteristics []struct {
					IID  uint64 `json:"iid"`
					Type string `json:"type"`
				} `json:"characteristics"`
			} `json:"services"`
		} `json:"accessories"`
	}
	if err := json.Unmarshal(accessories, &doc); err != nil {
		return "", err
	}
	typ = strings.ToUpper(strings.TrimLeft(typ, "0"))
	for _, a := range doc.Accessories {
		for _, s := range a.Services {
			for _, c := range s.Characteristics {
				t := strings.ToUpper(c.Type)
				if i := strings.IndexByte(t, '-'); i >= 0 {
					if t[i:] != "-0000-1000-8000-0026BB765291" {
						continue // vendor-specific type
					}
					t = t[:i]
				}
				if strings.TrimLeft(t, "0") == typ {
					return strconv.FormatUint(a.AID, 10) + "." + strconv.FormatUint(c.IID, 10), nil
				}
			}
		}
	}
	return "", errors.New("hapclient: characteristic type " + typ + " not found")
}
