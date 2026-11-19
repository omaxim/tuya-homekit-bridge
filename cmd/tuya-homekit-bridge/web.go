package main

// Optional, password-protected web UI for people without Apple devices.
// It is meant for the local network only (no TLS); do not expose it to the internet.

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

//go:embed web/index.html
var webFS embed.FS

var indexTmpl = template.Must(template.ParseFS(webFS, "web/index.html"))

type webServer struct {
	cfg  *WebConfig
	name string
	devs []*device

	mu       sync.Mutex
	sessions map[string]time.Time
}

const sessionCookie = "thb_session"
const sessionTTL = 30 * 24 * time.Hour

func startWeb(cfg *Config, devs []*device) {
	if cfg.Web == nil || cfg.Web.Listen == "" {
		return
	}
	w := &webServer{cfg: cfg.Web, name: cfg.Name, devs: devs, sessions: map[string]time.Time{}}
	if w.cfg.Title == "" {
		w.cfg.Title = cfg.Name
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", w.index)
	mux.HandleFunc("/login", w.login)
	mux.HandleFunc("/logout", w.logout)
	mux.HandleFunc("/api/state", w.auth(w.apiState))
	mux.HandleFunc("/api/switch", w.auth(w.apiSwitch))
	srv := &http.Server{Addr: w.cfg.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("web UI listening on %s", w.cfg.Listen)
		if err := srv.ListenAndServe(); err != nil {
			log.Printf("web UI stopped: %v", err)
		}
	}()
}

func (w *webServer) loggedIn(r *http.Request) bool {
	if w.cfg.Password == "" {
		return true
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	exp, ok := w.sessions[c.Value]
	if !ok || time.Now().After(exp) {
		delete(w.sessions, c.Value)
		return false
	}
	return true
}

// sameOrigin rejects cross-site POSTs (basic CSRF protection).
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true // same-origin form posts from older browsers
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

func (w *webServer) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if !w.loggedIn(r) {
			http.Error(rw, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && !sameOrigin(r) {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}
		h(rw, r)
	}
}

func (w *webServer) index(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(rw, r)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-store")
	indexTmpl.Execute(rw, map[string]any{
		"Title":    w.cfg.Title,
		"Lang":     map[bool]string{true: w.cfg.Lang, false: "en"}[w.cfg.Lang != ""],
		"LoggedIn": w.loggedIn(r),
		"Error":    r.URL.Query().Get("e") != "",
		"HasPass":  w.cfg.Password != "",
	})
}

func (w *webServer) login(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !sameOrigin(r) {
		http.Redirect(rw, r, "/", http.StatusSeeOther)
		return
	}
	pw := r.FormValue("password")
	if subtle.ConstantTimeCompare([]byte(pw), []byte(w.cfg.Password)) != 1 {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		log.Printf("web UI: failed login from %s", host)
		time.Sleep(time.Second) // slow down guessing
		http.Redirect(rw, r, "/?e=1", http.StatusSeeOther)
		return
	}
	b := make([]byte, 24)
	rand.Read(b)
	tok := hex.EncodeToString(b)
	w.mu.Lock()
	w.sessions[tok] = time.Now().Add(sessionTTL)
	w.mu.Unlock()
	http.SetCookie(rw, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds())})
	http.Redirect(rw, r, "/", http.StatusSeeOther)
}

func (w *webServer) logout(rw http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		w.mu.Lock()
		delete(w.sessions, c.Value)
		w.mu.Unlock()
	}
	http.SetCookie(rw, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(rw, r, "/", http.StatusSeeOther)
}

type apiAccessory struct {
	Device    int      `json:"device"`
	Index     int      `json:"index"`
	Type      string   `json:"type"`
	Name      string   `json:"name"`
	On        *bool    `json:"on,omitempty"`
	Value     *float64 `json:"value,omitempty"`
	Online    bool     `json:"online"`
	UpdatedAt string   `json:"updated_at,omitempty"`
}

func (w *webServer) state() []apiAccessory {
	var out []apiAccessory
	for di, d := range w.devs {
		dps, at := d.snapshot()
		for ai, a := range d.cfg.Accessories {
			item := apiAccessory{Device: di, Index: ai, Type: a.Type, Name: a.Name, Online: dps != nil}
			if !at.IsZero() {
				item.UpdatedAt = at.Format(time.RFC3339)
			}
			switch a.Type {
			case "switch":
				if dps != nil {
					on := matches(dps, a.State)
					item.On = &on
				}
			case "temperature":
				if v, ok := dps[a.DP].(float64); ok {
					if a.Scale > 0 {
						v /= a.Scale
					}
					item.Value = &v
				}
			}
			out = append(out, item)
		}
	}
	return out
}

func (w *webServer) apiState(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(rw).Encode(w.state())
}

func (w *webServer) apiSwitch(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "POST only", http.StatusMethodNotAllowed)
		return
	}
	di, err1 := strconv.Atoi(r.FormValue("device"))
	ai, err2 := strconv.Atoi(r.FormValue("index"))
	on := r.FormValue("on") == "true"
	if err1 != nil || err2 != nil || di < 0 || di >= len(w.devs) || ai < 0 || ai >= len(w.devs[di].cfg.Accessories) ||
		w.devs[di].cfg.Accessories[ai].Type != "switch" {
		http.Error(rw, "bad request", http.StatusBadRequest)
		return
	}
	d, a := w.devs[di], w.devs[di].cfg.Accessories[ai]
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	log.Printf("web UI: %s turned %q %s", host, a.Name, map[bool]string{true: "on", false: "off"}[on])
	if on {
		d.set(a.On, a.OnThen)
	} else {
		d.set(a.Off, a.OffThen)
	}
	w.apiState(rw, r)
}
