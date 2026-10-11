package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Push notifications. Every audit event is offered to the configured channels; a channel turns
// the ones it subscribed to into a human-readable message. Gotify (https://gotify.net) is the
// supported kind: self-hosted, so it works where only internal services are reachable.
//
// Adding another kind means one entry in notifierKinds — storage, filtering, retries, the API
// and the UI are shared.

type Notifier struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`            // see notifierKinds
	URL           string   `json:"url"`             // server base URL, e.g. https://gotify.example.com
	Token         string   `json:"token,omitempty"` // write-only: the Gotify application token
	HasToken      bool     `json:"hasToken"`
	Priority      int      `json:"priority"`      // 0-10, for routine events
	AlertPriority int      `json:"alertPriority"` // 0-10, for problems (see alertEvents)
	Events        []string `json:"events"`        // event names or prefixes like "repository.*"; empty = all
	Repos         []string `json:"repos"`         // empty = all
	Enabled       bool     `json:"enabled"`
	CreatedAt     int64    `json:"createdAt"`
	LastStatus    string   `json:"lastStatus"`
	LastDelivery  int64    `json:"lastDelivery"`
}

type notification struct {
	Title, Message, Link string
	Priority             int
	Event                Event
}

type notifyJob struct {
	n *Notifier
	e Event
}

var notifierKinds = map[string]func(a *App, ctx context.Context, n *Notifier, msg notification) (string, error){
	"gotify": (*App).sendGotify,
}

// alertEvents are delivered with the channel's alert priority: something needs a human.
var alertEvents = map[string]bool{
	"repository.degraded": true, "repository.quota": true, "task.failed": true,
	"login.lockout": true, "login.failed": true, "oidc.login.failed": true,
}

var eventTitles = map[string]string{
	"system.start":           "RepoMan started",
	"repository.degraded":    "Upstream unreachable",
	"repository.recovered":   "Upstream reachable again",
	"repository.quota":       "Storage quota reached",
	"task.failed":            "Scheduled task failed",
	"login.lockout":          "Sign-in blocked after repeated failures",
	"login.failed":           "Failed sign-in",
	"login":                  "Sign-in",
	"oidc.login":             "Single sign-on",
	"oidc.login.failed":      "Failed single sign-on",
	"oidc.link":              "SSO identity linked",
	"oidc.unlink":            "SSO identity unlinked",
	"password.change":        "Password changed",
	"password.reset":         "Password reset from the command line",
	"asset.upload":           "Artifact uploaded",
	"asset.delete":           "Artifact deleted",
	"component.delete":       "Component deleted",
	"npm.publish":            "npm package published",
	"pypi.upload":            "Python package uploaded",
	"docker.push":            "Docker image pushed",
	"docker.delete":          "Docker image deleted",
	"go.upload":              "Go module uploaded",
	"apt.upload":             "Debian package uploaded",
	"yum.upload":             "RPM package uploaded",
	"conda.upload":           "Conda package uploaded",
	"cargo.publish":          "Rust crate published",
	"pub.publish":            "Dart/Flutter package published",
	"alpine.upload":          "Alpine package uploaded",
	"lfs.upload":             "Git LFS object uploaded",
	"repository.create":      "Repository created",
	"repository.update":      "Repository changed",
	"repository.delete":      "Repository deleted",
	"repository.permissions": "Repository access changed",
	"repository.reindex":     "Repository index rebuilt",
	"cleanup":                "Cleanup finished",
	"cache.invalidate":       "Proxy cache invalidated",
	"user.create":            "User created",
	"user.update":            "User changed",
	"user.delete":            "User deleted",
	"token.create":           "API token created",
	"token.delete":           "API token revoked",
	"settings.update":        "Settings changed",
	"config.export":          "Configuration exported",
	"config.import":          "Configuration imported",
	"webhook.create":         "Webhook created",
	"webhook.update":         "Webhook changed",
	"webhook.delete":         "Webhook deleted",
	"notifier.create":        "Notification channel created",
	"notifier.update":        "Notification channel changed",
	"notifier.delete":        "Notification channel deleted",
	"notifier.test":          "Test notification",
	"task.run":               "Task started",
}

// eventMatches implements the event/repository filters shared by webhooks and notification channels.
// An event pattern is an exact name or a prefix ending in "*" ("repository.*", "login*").
func eventMatches(events, repos []string, e Event) bool {
	if len(events) > 0 && !slices.ContainsFunc(events, func(p string) bool {
		if prefix, ok := strings.CutSuffix(p, "*"); ok {
			return strings.HasPrefix(e.Event, prefix)
		}
		return p == e.Event
	}) {
		return false
	}
	if len(repos) == 0 {
		return true
	}
	for _, r := range repos {
		if e.Target == r || strings.HasPrefix(e.Target, r+"/") {
			return true
		}
	}
	return false
}

func (a *App) notifiers(where string, args ...any) ([]*Notifier, error) {
	rows, err := a.db.Query(`SELECT id, name, kind, url, token, priority, alert_priority, events, repos, enabled, created_at, last_status, last_delivery
		FROM notifiers `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Notifier{}
	for rows.Next() {
		n := &Notifier{}
		var ev, rp string
		if err := rows.Scan(&n.ID, &n.Name, &n.Kind, &n.URL, &n.Token, &n.Priority, &n.AlertPriority, &ev, &rp, &n.Enabled, &n.CreatedAt, &n.LastStatus, &n.LastDelivery); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(ev), &n.Events)
		json.Unmarshal([]byte(rp), &n.Repos)
		n.HasToken = n.Token != ""
		out = append(out, n)
	}
	return out, rows.Err()
}

// emitNotifications queues the event for every enabled channel subscribed to it. It never blocks:
// if the receiving server is so slow that the queue fills up, further notifications are dropped
// (the audit log still has every event).
func (a *App) emitNotifications(e Event) {
	if strings.HasPrefix(e.Event, "notifier.") { // a broken channel must not notify about itself
		return
	}
	list, err := a.notifiers(`WHERE enabled=1`)
	if err != nil {
		return
	}
	for _, n := range list {
		if !eventMatches(n.Events, n.Repos, e) {
			continue
		}
		select {
		case a.notifyQ <- notifyJob{n, e}:
		default:
			log.Printf("notification queue full: dropped %s for channel %q", e.Event, n.Name)
		}
	}
}

func (a *App) notifyWorker() {
	for job := range a.notifyQ {
		a.notify(job.n, job.e, true)
	}
}

// message renders an event for people: a short title, then what and who.
func (a *App) message(n *Notifier, e Event) notification {
	title := eventTitles[e.Event]
	if title == "" {
		title = e.Event
	}
	var b strings.Builder
	b.WriteString(e.Target)
	if e.Detail != "" {
		b.WriteString("\n" + e.Detail)
	}
	if e.Actor != "" && e.Actor != "system" {
		b.WriteString("\nby " + e.Actor)
	}
	msg := notification{Title: title, Message: b.String(), Priority: n.Priority, Event: e}
	if alertEvents[e.Event] {
		msg.Priority = n.AlertPriority
	}
	if base := a.settings().BaseURL; base != "" { // tapping the notification opens the right page
		msg.Link = base
		if name, _, _ := strings.Cut(e.Target, "/"); name != "" {
			if _, err := a.getRepo(name); err == nil {
				msg.Link = base + "/repositories/" + url.PathEscape(name)
			}
		}
	}
	return msg
}

// notify delivers one event (retrying twice with backoff if retry is set) and records the outcome.
func (a *App) notify(n *Notifier, e Event, retry bool) (status string, ok bool) {
	send := notifierKinds[n.Kind]
	if send == nil {
		return "unsupported channel type " + n.Kind, false
	}
	msg := a.message(n, e)
	waits := []time.Duration{0}
	if retry {
		waits = append(waits, 2*time.Second, 10*time.Second)
	}
	for attempt, wait := range waits {
		time.Sleep(wait)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		s, err := send(a, ctx, n, msg)
		cancel()
		if err == nil {
			status, ok = s, true
			break
		}
		status = err.Error()
		if len(waits) > 1 {
			status = fmt.Sprintf("%s (attempt %d)", status, attempt+1)
		}
		var perm permanentError
		if errors.As(err, &perm) { // wrong token or URL: retrying cannot help
			break
		}
	}
	a.db.Exec(`UPDATE notifiers SET last_status=?, last_delivery=? WHERE id=?`, status, time.Now().Unix(), n.ID)
	return status, ok
}

type permanentError struct{ error }

// sendGotify posts a message to a Gotify server: POST <url>/message with the application token.
func (a *App) sendGotify(ctx context.Context, n *Notifier, msg notification) (string, error) {
	extras := map[string]any{"repoman::event": msg.Event}
	if msg.Link != "" {
		extras["client::notification"] = map[string]any{"click": map[string]string{"url": msg.Link}}
	}
	body, _ := json.Marshal(map[string]any{"title": msg.Title, "message": msg.Message, "priority": msg.Priority, "extras": extras})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(n.URL, "/")+"/message", bytes.NewReader(body))
	if err != nil {
		return "", permanentError{err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "RepoMan/"+version)
	req.Header.Set("X-Gotify-Key", n.Token)
	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach the Gotify server: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	switch {
	case resp.StatusCode < 300:
		return resp.Status, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return "", permanentError{fmt.Errorf("%s: Gotify rejected the application token", resp.Status)}
	case resp.StatusCode == http.StatusNotFound:
		return "", permanentError{fmt.Errorf("%s: no Gotify API at this URL (use the server's base URL, without /message)", resp.Status)}
	}
	return "", fmt.Errorf("gotify answered %s", resp.Status)
}

// ---------- API ----------

func (a *App) listNotifiersAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	list, err := a.notifiers("")
	for _, n := range list {
		n.Token = ""
	}
	return list, err
}

func (a *App) saveNotifierAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	var n Notifier
	if err := readJSON(r, &n); err != nil {
		return nil, err
	}
	n.Name, n.URL, n.Token = strings.TrimSpace(n.Name), strings.TrimSpace(n.URL), strings.TrimSpace(n.Token)
	if n.Kind == "" {
		n.Kind = "gotify"
	}
	if notifierKinds[n.Kind] == nil {
		return nil, httpErr{400, "unsupported channel type " + n.Kind + " (supported: gotify)"}
	}
	if n.Name == "" || len(n.Name) > 100 {
		return nil, httpErr{400, "name is required (at most 100 characters)"}
	}
	if pu, err := url.Parse(n.URL); err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" || pu.RawQuery != "" {
		return nil, httpErr{400, "server URL must be an absolute http(s) URL such as https://gotify.example.com"}
	}
	if n.Priority < 0 || n.Priority > 10 || n.AlertPriority < 0 || n.AlertPriority > 10 {
		return nil, httpErr{400, "priorities must be between 0 and 10"}
	}
	ev, rp := jsonString(nonNil(n.Events)), jsonString(nonNil(n.Repos))
	if id := r.PathValue("id"); id != "" {
		q := `UPDATE notifiers SET name=?, kind=?, url=?, priority=?, alert_priority=?, events=?, repos=?, enabled=? WHERE id=?`
		args := []any{n.Name, n.Kind, n.URL, n.Priority, n.AlertPriority, ev, rp, n.Enabled, id}
		if n.Token != "" { // blank keeps the stored token
			q = `UPDATE notifiers SET name=?, kind=?, url=?, priority=?, alert_priority=?, events=?, repos=?, enabled=?, token=? WHERE id=?`
			args = []any{n.Name, n.Kind, n.URL, n.Priority, n.AlertPriority, ev, rp, n.Enabled, n.Token, id}
		}
		res, err := a.db.Exec(q, args...)
		if err != nil {
			return nil, err
		}
		if c, _ := res.RowsAffected(); c == 0 {
			return nil, errNotFound
		}
		a.audit(u, "notifier.update", n.Name)
		return nil, nil
	}
	if n.Token == "" {
		return nil, httpErr{400, "the application token is required (Gotify: Apps → Create application)"}
	}
	res, err := a.db.Exec(`INSERT INTO notifiers(name, kind, url, token, priority, alert_priority, events, repos, enabled, created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		n.Name, n.Kind, n.URL, n.Token, n.Priority, n.AlertPriority, ev, rp, n.Enabled, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	a.audit(u, "notifier.create", n.Name)
	return map[string]int64{"id": id}, nil
}

func (a *App) deleteNotifierAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	res, err := a.db.Exec(`DELETE FROM notifiers WHERE id=?`, r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return nil, errNotFound
	}
	a.audit(u, "notifier.delete", r.PathValue("id"))
	return nil, nil
}

// testNotifierAPI sends a test message right away and reports what the server answered.
func (a *App) testNotifierAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	list, err := a.notifiers(`WHERE id=?`, r.PathValue("id"))
	if err != nil || len(list) == 0 {
		return nil, errNotFound
	}
	status, ok := a.notify(list[0], Event{Event: "notifier.test", Actor: u.Username, Target: list[0].Name,
		Detail: "If you can read this, RepoMan can reach your notification server.", Timestamp: time.Now().Unix()}, false)
	return map[string]any{"ok": ok, "status": status}, nil
}
