//go:build !unix

package tofu

import "sync"

var initMu sync.Mutex

func lockFile(string) (func(), error) {
	initMu.Lock()
	return initMu.Unlock, nil
}
