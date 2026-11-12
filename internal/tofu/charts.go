package tofu

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
	"sigs.k8s.io/yaml"
)

//go:embed charts.json
var chartsJSON []byte

const TalosBackupVersion = "v0.1.0-beta.2"

type Step struct {
	Version     string `json:"version"`
	Digest      string `json:"digest"`
	KubeVersion string `json:"kubeVersion"`
	GatewayAPI  string `json:"gatewayAPI,omitempty"`
}

type Chart struct {
	Repo       string `json:"repo"`
	Chart      string `json:"chart"`
	StepMinors bool   `json:"stepMinors"`
	Steps      []Step `json:"steps"`
}

var Charts = mustCharts()

var GatewayAPIVersion = mustGatewayAPI()

func mustCharts() map[string]Chart {
	var out map[string]Chart
	if err := json.Unmarshal(chartsJSON, &out); err != nil {
		panic("charts.json: " + err.Error())
	}
	for name, c := range out {
		if len(c.Steps) == 0 {
			panic("charts.json: " + name + " has no steps")
		}
		for i, s := range c.Steps {
			v, err := semver.NewVersion(s.Version)
			if err != nil {
				panic(fmt.Sprintf("charts.json: %s %s: %v", name, s.Version, err))
			}
			if i > 0 && !v.GreaterThan(semver.MustParse(c.Steps[i-1].Version)) {
				panic(fmt.Sprintf("charts.json: %s steps are not oldest first", name))
			}
		}
	}
	return out
}

func mustGatewayAPI() string {
	b, err := templates.ReadFile(templateRoot + "/charts/gateway-api/Chart.yaml")
	if err != nil {
		panic(err)
	}
	var meta struct {
		AppVersion string `json:"appVersion"`
	}
	if err := yaml.Unmarshal(b, &meta); err != nil {
		panic("gateway-api Chart.yaml: " + err.Error())
	}
	return meta.AppVersion
}

func NewestVersions() map[string]string {
	out := make(map[string]string, len(Charts))
	for name, c := range Charts {
		out[name] = c.Newest().Version
	}
	return out
}

func PinnedVersion(addon string) string {
	if addon == "talos-backup" {
		return TalosBackupVersion
	}
	if c, ok := Charts[addon]; ok {
		return c.Newest().Version
	}
	return ""
}

func (c Chart) Newest() Step { return c.Steps[len(c.Steps)-1] }

type Target struct {
	Step Step
	N    int
	Of   int
}

func (c Chart) Target(name, installed string) (Target, error) {
	newest := Target{Step: c.Newest()}
	iv, err := semver.NewVersion(installed)
	if !c.StepMinors || installed == "" || err != nil || !iv.LessThan(semver.MustParse(newest.Step.Version)) {
		return newest, nil
	}
	var ahead []Step
	for _, s := range c.Steps {
		sv := semver.MustParse(s.Version)
		if sv.Major() > iv.Major() || sv.Major() == iv.Major() && sv.Minor() > iv.Minor() {
			ahead = append(ahead, s)
		}
	}
	if len(ahead) == 0 {
		return newest, nil
	}
	next := semver.MustParse(ahead[0].Version)
	if next.Major() == iv.Major() && next.Minor() > iv.Minor()+1 {
		return Target{}, fmt.Errorf("%s %s is more than one minor behind %s, the oldest step Kubit ships.", name, installed, ahead[0].Version)
	}
	return Target{Step: ahead[0], N: 1, Of: len(ahead)}, nil
}

func (s Step) Fits(name, kubernetes string) error {
	if s.KubeVersion == "" {
		return nil
	}
	want, err := semver.NewConstraint(s.KubeVersion)
	if err != nil {
		return fmt.Errorf("%s %s: kubeVersion %q: %w", name, s.Version, s.KubeVersion, err)
	}
	have, err := semver.NewVersion(kubernetes)
	if err != nil {
		return fmt.Errorf("Kubernetes version %q: %w", kubernetes, err)
	}
	if !want.Check(have) {
		return fmt.Errorf("%s %s needs Kubernetes %s.", name, s.Version, strings.ReplaceAll(strings.ReplaceAll(s.KubeVersion, "-0", ""), " ", ""))
	}
	return nil
}

func (s Step) GatewayAPIFits(name string) error {
	if s.GatewayAPI == "" {
		return nil
	}
	want, err := semver.NewVersion(s.GatewayAPI)
	if err != nil {
		return fmt.Errorf("%s %s: gatewayAPI %q: %w", name, s.Version, s.GatewayAPI, err)
	}
	have := semver.MustParse(GatewayAPIVersion)
	if want.Major() != have.Major() || want.Minor() != have.Minor() {
		return fmt.Errorf("%s %s needs Gateway API v%d.%d; Kubit ships %s.", name, s.Version, want.Major(), want.Minor(), GatewayAPIVersion)
	}
	return nil
}
