package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/store"
)

// forwardEvent sends a health event to the configured sinks; failures are logged and
// never block the watcher. Only events at or above the configured severity go out.
func (s *Server) forwardEvent(e store.EventRow) {
	v, err := s.store.GetSettings(context.Background())
	if err != nil {
		return
	}
	if !severityAtLeast(e.Severity, v.Alerts.MinSeverity) {
		return
	}
	if v.Alerts.WebhookURL != "" {
		go func() {
			if err := postWebhook(v.Alerts.WebhookURL, e); err != nil {
				log.Printf("alert webhook: %v", err)
			}
		}()
	}
	if v.Alerts.SMTP.Host != "" && len(v.Alerts.SMTP.To) > 0 {
		go func() {
			if err := sendMail(v.Alerts.SMTP, e); err != nil {
				log.Printf("alert mail: %v", err)
			}
		}()
	}
}

func severityAtLeast(sev, min string) bool {
	rank := map[string]int{"info": 0, "warn": 1, "critical": 2}
	m, ok := rank[min]
	if !ok {
		m = 1
	}
	return rank[sev] >= m
}

// webhookPayload is a generic JSON body; Slack/Discord/Teams incoming webhooks accept
// the top-level "text" field, everything else is for programmatic receivers.
type webhookPayload struct {
	Text     string         `json:"text"`
	Source   string         `json:"source"`
	Event    store.EventRow `json:"event"`
	Severity string         `json:"severity"`
	Cluster  string         `json:"cluster"`
}

func postWebhook(url string, e store.EventRow) error {
	body, _ := json.Marshal(webhookPayload{Text: alertText(e), Source: "kubit", Event: e, Severity: e.Severity, Cluster: e.Cluster})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return nil
}

func alertText(e store.EventRow) string {
	node := ""
	if e.Node != "" {
		node = " " + e.Node
	}
	return fmt.Sprintf("[kubit %s] %s%s: %s", strings.ToUpper(e.Severity), e.Cluster, node, e.Message)
}

func sendMail(c store.SMTP, e store.EventRow) error {
	port := c.Port
	if port == 0 {
		port = 587
	}
	addr := fmt.Sprintf("%s:%d", c.Host, port)
	msg := strings.Join([]string{
		"From: " + c.From,
		"To: " + strings.Join(c.To, ", "),
		"Subject: " + alertText(e),
		"Content-Type: text/plain; charset=utf-8",
		"",
		fmt.Sprintf("Cluster: %s\nNode: %s\nSeverity: %s\nKind: %s\nTime: %s\n\n%s\n", e.Cluster, e.Node, e.Severity, e.Kind, e.TS, e.Message),
	}, "\r\n")
	var auth smtp.Auth
	if c.Username != "" {
		auth = smtp.PlainAuth("", c.Username, c.Password, c.Host)
	}
	return smtp.SendMail(addr, auth, c.From, c.To, []byte(msg))
}

// handleAlertTest sends a synthetic critical event through the configured sinks.
func (s *Server) handleAlertTest(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.GetSettings(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	e := store.EventRow{TS: time.Now().UTC().Format(time.RFC3339), Cluster: "kubit", Severity: "critical", Kind: "test", Message: "Test alert from Kubit settings — forwarding works."}
	var errs []string
	if v.Alerts.WebhookURL != "" {
		if err := postWebhook(v.Alerts.WebhookURL, e); err != nil {
			errs = append(errs, "webhook: "+err.Error())
		}
	}
	if v.Alerts.SMTP.Host != "" && len(v.Alerts.SMTP.To) > 0 {
		if err := sendMail(v.Alerts.SMTP, e); err != nil {
			errs = append(errs, "smtp: "+err.Error())
		}
	}
	if v.Alerts.WebhookURL == "" && v.Alerts.SMTP.Host == "" {
		errs = append(errs, "no sink configured")
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(errs) == 0, "errors": errs})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListAudit(r.Context(), r.URL.Query().Get("cluster"), 1000)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
