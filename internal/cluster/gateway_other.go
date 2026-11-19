//go:build !darwin && !linux

package cluster

func defaultRoute() (string, string) { return "", "" }
