package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/mikaelhug/kubit/internal/watch"
)

func (s *Server) forwardEvent(e watch.Event) {
	v := s.settings.Alerts
	if v.WebhookURL == "" || (e.Recovers == "" && !watch.AtLeast(e.Severity, v.MinSeverity)) {
		return
	}
	go func() {
		if err := postWebhook(v.WebhookURL, e); err != nil {
			log.Printf("alert webhook: %v", err)
		}
	}()
}

type webhookPayload struct {
	Text     string      `json:"text"`
	Source   string      `json:"source"`
	Event    watch.Event `json:"event"`
	Severity string      `json:"severity"`
	Cluster  string      `json:"cluster"`
	Resolves string      `json:"resolves,omitempty"`
}

func postWebhook(url string, e watch.Event) error {
	body, _ := json.Marshal(webhookPayload{Text: alertText(e), Source: "kubit", Event: e, Severity: e.Severity, Cluster: e.Cluster, Resolves: e.Recovers})
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

func alertText(e watch.Event) string {
	node := ""
	if e.Node != "" {
		node = " " + e.Node
	}
	label := strings.ToUpper(e.Severity)
	if e.Recovers != "" {
		label = "RESOLVED"
	}
	return fmt.Sprintf("[kubit %s] %s%s: %s", label, e.Cluster, node, e.Message)
}
