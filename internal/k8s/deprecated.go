package k8s

import (
	"bufio"
	"context"
	"fmt"
	"regexp"
	"strings"

	utilversion "k8s.io/apimachinery/pkg/util/version"
)

type DeprecatedAPI struct {
	Group          string `json:"group"`
	Version        string `json:"version"`
	Resource       string `json:"resource"`
	RemovedRelease string `json:"removedRelease"`
}

var deprecatedLine = regexp.MustCompile(`^apiserver_requested_deprecated_apis\{([^}]*)\}\s+(\S+)`)
var labelPair = regexp.MustCompile(`(\w+)="([^"]*)"`)

func (c *Client) DeprecatedAPIs(ctx context.Context) ([]DeprecatedAPI, error) {
	body, err := c.Discovery().RESTClient().Get().AbsPath("/metrics").DoRaw(ctx)
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

func (d DeprecatedAPI) RemovedBy(version string) bool {
	if d.RemovedRelease == "" {
		return false
	}
	removed, err := utilversion.ParseMajorMinor(d.RemovedRelease)
	if err != nil {
		return false
	}
	target, err := utilversion.ParseMajorMinor(version)
	return err == nil && target.AtLeast(removed)
}

func (d DeprecatedAPI) String() string {
	g := d.Group
	if g == "" {
		g = "core"
	}
	return fmt.Sprintf("%s/%s %s (removed in %s)", g, d.Version, d.Resource, d.RemovedRelease)
}
