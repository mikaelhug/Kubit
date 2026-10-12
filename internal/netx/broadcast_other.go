//go:build !darwin && !linux

package netx

func setBroadcast(int) error { return nil }
