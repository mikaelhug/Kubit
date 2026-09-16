//go:build !darwin && !linux

package pxe

func setReuse(int) error { return nil }
