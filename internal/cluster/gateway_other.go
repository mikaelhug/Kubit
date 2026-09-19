//go:build !darwin && !linux

package cluster

func DefaultGateway() string { return "" }
