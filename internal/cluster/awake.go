package cluster

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
)

func KeepAwake() func() {
	if runtime.GOOS != "darwin" {
		return func() {}
	}
	cmd := exec.Command("caffeinate", "-i", "-w", strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		return func() {}
	}
	return func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}
