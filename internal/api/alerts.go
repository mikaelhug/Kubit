package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/store"
)

func (s *Server) alertRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/audit", s.handleAudit)
}

func (s *Server) forwardEvent(e store.EventRow) {
	v, err := s.store.GetSettings(context.Background())
	if err != nil {
		return
	}
	if !severityAtLeast(e.Severity, v.Alerts.MinSeverity) {
		return
	}
	s.deliver(v, e)
}

func (s *Server) deliver(v store.Settings, e store.EventRow) {
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

const smtpConversation = time.Minute

func sendMail(c store.SMTP, e store.EventRow) error {
	port := c.Port
	if port == 0 {
		port = 587
	}
	addr := net.JoinHostPort(c.Host, strconv.Itoa(port))
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
	var (
		cl  *smtp.Client
		err error
	)
	dialer := net.Dialer{Timeout: 15 * time.Second}
	deadline := time.Now().Add(smtpConversation)
	switch c.Mode() {
	case "tls":
		conn, derr := tls.DialWithDialer(&dialer, "tcp", addr, &tls.Config{ServerName: c.Host})
		if derr != nil {
			return derr
		}
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close()
			return err
		}
		cl, err = smtp.NewClient(conn, c.Host)
	default:
		conn, derr := dialer.Dial("tcp", addr)
		if derr != nil {
			return derr
		}
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close()
			return err
		}
		cl, err = smtp.NewClient(conn, c.Host)
		if err == nil && c.Mode() == "starttls" {
			if ok, _ := cl.Extension("STARTTLS"); !ok {
				cl.Close()
				return fmt.Errorf("%s does not offer STARTTLS; choose implicit TLS or none", addr)
			}
			err = cl.StartTLS(&tls.Config{ServerName: c.Host})
		}
	}
	if err != nil {
		return err
	}
	defer cl.Close()
	if auth != nil {
		if err := cl.Auth(auth); err != nil {
			return err
		}
	}
	if err := cl.Mail(c.From); err != nil {
		return err
	}
	for _, to := range c.To {
		if err := cl.Rcpt(to); err != nil {
			return err
		}
	}
	wc, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write([]byte(msg)); err != nil {
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return cl.Quit()
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListAudit(r.Context(), r.URL.Query().Get("cluster"), 1000)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
