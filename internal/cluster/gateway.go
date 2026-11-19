package cluster

func DefaultGateway() string {
	gw, _ := defaultRoute()
	return gw
}

func DefaultInterface() string {
	_, iface := defaultRoute()
	return iface
}
