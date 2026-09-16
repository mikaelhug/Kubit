// labssh runs a command on a lab host with Kubit's SSH key (dev/debug only):
//   go run ./hack/labssh <host-ip> '<command>'
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/store"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: labssh <host> <command>")
		os.Exit(2)
	}
	dir := os.Getenv("KUBIT_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".kubit")
	}
	crypto, err := store.LoadCrypto()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	s, err := store.Open(dir, crypto)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer s.Close()
	priv, _, err := s.SSHKey(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	c, err := labhost.Dial(context.Background(), os.Args[1], priv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer c.Close()
	out, err := c.Run(context.Background(), os.Args[2])
	fmt.Print(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
