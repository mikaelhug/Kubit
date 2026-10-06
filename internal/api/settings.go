package api

type Settings struct {
	FactoryURL       string   `json:"factoryUrl"`
	DiscoverySubnets []string `json:"discoverySubnets"`
	PXEStatusURL     string   `json:"pxeStatusUrl"`
	PXERunDir        string   `json:"-"`
	Alerts           Alerts   `json:"alerts"`
}

type Alerts struct {
	MinSeverity string `json:"minSeverity"`
	WebhookURL  string `json:"webhookUrl"`
}

func DefaultSettings() Settings {
	return Settings{FactoryURL: "https://factory.talos.dev", DiscoverySubnets: []string{}, PXEStatusURL: "http://127.0.0.1:8069/status.json", Alerts: Alerts{MinSeverity: "warn"}}
}
