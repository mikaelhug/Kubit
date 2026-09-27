//go:build !darwin && !linux

package pxe

func setReuse(int) error { return nil }

func bindDevice(int, string) error { return nil }
