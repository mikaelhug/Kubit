package k8s

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type HelmRelease struct {
	Name         string `json:"name"`
	Namespace    string `json:"namespace"`
	Revision     int    `json:"revision"`
	Chart        string `json:"chart"`
	ChartVersion string `json:"chartVersion"`
	AppVersion   string `json:"appVersion,omitempty"`
	Status       string `json:"status"`
	LastDeployed int64  `json:"lastDeployed,omitempty"`
}

func (c *Client) HelmRelease(ctx context.Context, namespace, name string) (*HelmRelease, error) {
	list, err := c.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{LabelSelector: "owner=helm,name=" + name})
	if err != nil {
		return nil, err
	}
	latest, best := -1, 0
	for i, s := range list.Items {
		if v, err := strconv.Atoi(s.Labels["version"]); err == nil && v > best {
			latest, best = i, v
		}
	}
	if latest < 0 {
		return nil, nil
	}
	return DecodeHelmRelease(list.Items[latest].Data["release"])
}

var gzipMagic = []byte{0x1f, 0x8b, 0x08}

func DecodeHelmRelease(data []byte) (*HelmRelease, error) {
	raw, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, fmt.Errorf("helm release: %w", err)
	}
	if bytes.HasPrefix(raw, gzipMagic) {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("helm release: %w", err)
		}
		if raw, err = io.ReadAll(zr); err != nil {
			return nil, fmt.Errorf("helm release: %w", err)
		}
	}
	var rel struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Version   int    `json:"version"`
		Info      struct {
			Status       string    `json:"status"`
			LastDeployed time.Time `json:"last_deployed"`
		} `json:"info"`
		Chart struct {
			Metadata struct {
				Name       string `json:"name"`
				Version    string `json:"version"`
				AppVersion string `json:"appVersion"`
			} `json:"metadata"`
		} `json:"chart"`
	}
	if err := json.Unmarshal(raw, &rel); err != nil {
		return nil, fmt.Errorf("helm release: %w", err)
	}
	out := &HelmRelease{Name: rel.Name, Namespace: rel.Namespace, Revision: rel.Version, Chart: rel.Chart.Metadata.Name, ChartVersion: rel.Chart.Metadata.Version, AppVersion: rel.Chart.Metadata.AppVersion, Status: rel.Info.Status}
	if !rel.Info.LastDeployed.IsZero() {
		out.LastDeployed = rel.Info.LastDeployed.Unix()
	}
	return out, nil
}
