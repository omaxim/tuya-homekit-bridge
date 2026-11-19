package tuya

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"net"
	"sync"
	"time"
)

// udpKey is the well-known key Tuya devices use for LAN discovery messages.
var udpKey = func() []byte { s := md5.Sum([]byte("yGAdlopoPVldABfn")); return s[:] }()

// CmdReqDevInfo asks protocol 3.5 devices to announce themselves.
const CmdReqDevInfo = 0x25

// Announcement is a device discovery message.
type Announcement struct {
	IP        string `json:"ip"`
	ID        string `json:"gwId"`
	ProductID string `json:"productKey"`
	Version   string `json:"version"`
}

// Discover finds protocol 3.5 devices on the LAN until ctx is cancelled.
//
// Protocol 3.5 devices stay quiet until asked, so Discover periodically
// broadcasts a request on UDP 7000 (like the Tuya app does) and listens for
// replies on UDP 7000 and for passive announcements on UDP 6667.
func Discover(ctx context.Context, fn func(Announcement)) error {
	var mu sync.Mutex
	report := func(a Announcement) { mu.Lock(); defer mu.Unlock(); fn(a) }

	var listeners []net.PacketConn
	for _, port := range []string{":7000", ":6667"} {
		pc, err := net.ListenPacket("udp4", port)
		if err != nil {
			for _, l := range listeners {
				l.Close()
			}
			return err
		}
		listeners = append(listeners, pc)
	}
	var wg sync.WaitGroup
	for _, pc := range listeners {
		wg.Add(1)
		go func(pc net.PacketConn) {
			defer wg.Done()
			listen(ctx, pc, report)
		}(pc)
	}
	go func() {
		t := time.NewTicker(6 * time.Second)
		defer t.Stop()
		for {
			sendDiscoveryRequests()
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	<-ctx.Done()
	for _, pc := range listeners {
		pc.Close()
	}
	wg.Wait()
	return nil
}

func listen(ctx context.Context, pc net.PacketConn, fn func(Announcement)) {
	buf := make([]byte, 4096)
	for ctx.Err() == nil {
		pc.SetReadDeadline(time.Now().Add(time.Second))
		n, _, err := pc.ReadFrom(buf)
		if err != nil || n < headerLen {
			continue
		}
		_, p, err := decodeFrame(udpKey, buf[:n])
		if err != nil {
			continue
		}
		var a Announcement
		if json.Unmarshal(p, &a) == nil && a.ID != "" && a.IP != "" {
			fn(a)
		}
	}
}

// sendDiscoveryRequests broadcasts a REQ_DEVINFO message on every IPv4 interface.
func sendDiscoveryRequests() {
	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagBroadcast == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil {
				continue
			}
			ip := ipn.IP.To4()
			bcast := make(net.IP, 4)
			for i := range bcast {
				bcast[i] = ip[i] | ^ipn.Mask[i]
			}
			body, _ := json.Marshal(map[string]string{"from": "app", "ip": ip.String()})
			frame, err := encodeFrame(udpKey, 0, CmdReqDevInfo, body)
			if err != nil {
				continue
			}
			c, err := net.DialUDP("udp4", &net.UDPAddr{IP: ip}, &net.UDPAddr{IP: bcast, Port: 7000})
			if err != nil {
				continue
			}
			c.Write(frame)
			c.Close()
		}
	}
}

// FindIP waits up to timeout for the device with the given id to announce itself.
func FindIP(id string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var ip string
	err := Discover(ctx, func(a Announcement) {
		if a.ID == id && ip == "" {
			ip = a.IP
			cancel()
		}
	})
	if ip == "" && err == nil {
		err = context.DeadlineExceeded
	}
	return ip, err
}
