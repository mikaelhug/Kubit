package k8s

import (
	"bufio"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// DeprecatedAPI is a deprecated API group/version that clients have used since the
// API server started, as reported by the apiserver_requested_deprecated_apis metric.
type DeprecatedAPI struct {
	Group          string `json:"group"`
	Version        string `json:"version"`
	Resource       string `json:"resource"`
	RemovedRelease string `json:"removedRelease"` // e.g. "1.32"
}

var deprecatedLine = regexp.MustCompile(`^apiserver_requested_deprecated_apis\{([^}]*)\}\s+(\S+)`)
var labelPair = regexp.MustCompile(`(\w+)="([^"]*)"`)

// DeprecatedAPIs scrapes the API server metrics for deprecated API usage.
func (c *Client) DeprecatedAPIs(ctx context.Context) ([]DeprecatedAPI, error) {
	body, err := c.RESTClient().Get().AbsPath("/metrics").DoRaw(ctx)
	if err != nil {
		return nil, err
	}
	var out []DeprecatedAPI
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	sc.Buffer(make([]byte, 1024*1024), 4*1024*1024)
	for sc.Scan() {
		m := deprecatedLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		var d DeprecatedAPI
		for _, kv := range labelPair.FindAllStringSubmatch(m[1], -1) {
			switch kv[1] {
			case "group":
				d.Group = kv[2]
			case "version":
				d.Version = kv[2]
			case "resource":
				d.Resource = kv[2]
			case "removed_release":
				d.RemovedRelease = kv[2]
			}
		}
		out = append(out, d)
	}
	return out, sc.Err()
}

// RemovedBy reports whether the API is gone in the given Kubernetes version ("v1.34.0").
func (d DeprecatedAPI) RemovedBy(version string) bool {
	if d.RemovedRelease == "" {
		return false
	}
	rMaj, rMin, ok1 := minor(d.RemovedRelease)
	tMaj, tMin, ok2 := minor(version)
	if !ok1 || !ok2 {
		return false
	}
	return tMaj > rMaj || (tMaj == rMaj && tMin >= rMin)
}

func (d DeprecatedAPI) String() string {
	g := d.Group
	if g == "" {
		g = "core"
	}
	return fmt.Sprintf("%s/%s %s (removed in %s)", g, d.Version, d.Resource, d.RemovedRelease)
}

func minor(v string) (int, int, bool) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	a, err1 := strconv.Atoi(parts[0])
	b, err2 := strconv.Atoi(parts[1])
	return a, b, err1 == nil && err2 == nil
}
