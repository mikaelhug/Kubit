package cluster

import "fmt"

func HumanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	v := float64(b) / float64(div)
	if exp >= 2 && v < 100 {
		return fmt.Sprintf("%.1f%c", v, "KMGTPE"[exp])
	}
	return fmt.Sprintf("%.0f%c", v, "KMGTPE"[exp])
}
