package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Every audit event is offered to webhooks. Deliveries are POSTed as JSON with an
// X-RepoMan-Signature: sha256=<hmac of body with the webhook secret> header.

type Event struct {
	Event     string `json:"event"`
	Actor     string `json:"actor"`
	Target    string `json:"target"`
	Detail    string `json:"detail,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

type Webhook struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	URL          string   `json:"url"`
	Secret       string   `json:"secret,omitempty"` // write-only
	HasSecret    bool     `json:"hasSecret"`
	Events       []string `json:"events"` // empty = all
	Repos        []string `json:"repos"`  // empty = all
	Enabled      bool     `json:"enabled"`
	CreatedAt    int64    `json:"createdAt"`
	LastStatus   string   `json:"lastStatus"`
	LastDelivery int64    `json:"lastDelivery"`
}

func (a *App) webhooks(where string, args ...any) ([]*Webhook, error) {
	rows, err := a.db.Query(`SELECT id, name, url, secret, events, repos, enabled, created_at, last_status, last_delivery FROM webhooks `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Webhook{}
	for rows.Next() {
		h := &Webhook{}
		var ev, rp string
		if err := rows.Scan(&h.ID, &h.Name, &h.URL, &h.Secret, &ev, &rp, &h.Enabled, &h.CreatedAt, &h.LastStatus, &h.LastDelivery); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(ev), &h.Events)
		json.Unmarshal([]byte(rp), &h.Repos)
		h.HasSecret = h.Secret != ""
		out = append(out, h)
	}
	return out, rows.Err()
}

func (h *Webhook) matches(e Event) bool { return eventMatches(h.Events, h.Repos, e) }

func (a *App) emitWebhooks(e Event) {
	hooks, err := a.webhooks(`WHERE enabled=1`)
	if err != nil || len(hooks) == 0 {
		return
	}
	for _, h := range hooks {
		if h.matches(e) {
			go a.deliver(h, e, true)
		}
	}
}

// deliver POSTs the event (retrying twice with backoff if retry is set) and records the outcome.
func (a *App) deliver(h *Webhook, e Event, retry bool) string {
	body, _ := json.Marshal(e)
	status := ""
	waits := []time.Duration{0}
	if retry {
		waits = append(waits, 2*time.Second, 10*time.Second)
	}
	for attempt, wait := range waits {
		time.Sleep(wait)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "RepoMan-Webhook/"+version)
		req.Header.Set("X-RepoMan-Event", e.Event)
		if h.Secret != "" {
			mac := hmac.New(sha256.New, []byte(h.Secret))
			mac.Write(body)
			req.Header.Set("X-RepoMan-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		}
		resp, err := a.client.Do(req)
		cancel()
		if err != nil {
			status = "error: " + err.Error()
		} else {
			resp.Body.Close()
			status = resp.Status
			if resp.StatusCode < 300 {
				break
			}
		}
		status = fmt.Sprintf("%s (attempt %d)", status, attempt+1)
	}
	a.db.Exec(`UPDATE webhooks SET last_status=?, last_delivery=? WHERE id=?`, status, time.Now().Unix(), h.ID)
	return status
}

func (a *App) listWebhooksAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	hooks, err := a.webhooks("")
	for _, h := range hooks {
		h.Secret = ""
	}
	return hooks, err
}

func (a *App) saveWebhookAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	var h Webhook
	if err := readJSON(r, &h); err != nil {
		return nil, err
	}
	if pu, err := url.Parse(h.URL); err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
		return nil, httpErr{400, "webhook URL must be an absolute http(s) URL"}
	}
	if strings.TrimSpace(h.Name) == "" {
		return nil, httpErr{400, "name is required"}
	}
	ev, _ := json.Marshal(nonNil(h.Events))
	rp, _ := json.Marshal(nonNil(h.Repos))
	if id := r.PathValue("id"); id != "" {
		q := `UPDATE webhooks SET name=?, url=?, events=?, repos=?, enabled=? WHERE id=?`
		args := []any{h.Name, h.URL, string(ev), string(rp), h.Enabled, id}
		if h.Secret != "" { // blank keeps the existing secret
			q = `UPDATE webhooks SET name=?, url=?, events=?, repos=?, enabled=?, secret=? WHERE id=?`
			args = []any{h.Name, h.URL, string(ev), string(rp), h.Enabled, h.Secret, id}
		}
		if _, err := a.db.Exec(q, args...); err != nil {
			return nil, err
		}
		a.audit(u, "webhook.update", h.Name)
		return nil, nil
	}
	_, err := a.db.Exec(`INSERT INTO webhooks(name, url, secret, events, repos, enabled, created_at) VALUES(?,?,?,?,?,?,?)`,
		h.Name, h.URL, h.Secret, string(ev), string(rp), h.Enabled, time.Now().Unix())
	if err == nil {
		a.audit(u, "webhook.create", h.Name)
	}
	return nil, err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (a *App) deleteWebhookAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	if _, err := a.db.Exec(`DELETE FROM webhooks WHERE id=?`, r.PathValue("id")); err != nil {
		return nil, err
	}
	a.audit(u, "webhook.delete", r.PathValue("id"))
	return nil, nil
}

func (a *App) testWebhookAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	hooks, err := a.webhooks(`WHERE id=?`, r.PathValue("id"))
	if err != nil || len(hooks) == 0 {
		return nil, errNotFound
	}
	status := a.deliver(hooks[0], Event{Event: "webhook.test", Actor: u.Username, Target: hooks[0].Name, Detail: "test delivery", Timestamp: time.Now().Unix()}, false)
	return map[string]string{"status": status}, nil
}
