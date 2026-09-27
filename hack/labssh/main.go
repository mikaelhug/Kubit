package main

import (
	"context"
	"fmt"
	"os"

	"github.com/mikael/kubit/internal/labhost/libvirt"
	"github.com/mikael/kubit/internal/store"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: labssh <host> <command>")
		os.Exit(2)
	}
	dir, err := store.HomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
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
	c, err := libvirt.Dial(context.Background(), os.Args[1], priv)
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
