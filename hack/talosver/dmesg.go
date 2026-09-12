package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/client"
)

func dmesg(ip string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := client.New(ctx, client.WithEndpoints(ip), client.WithTLSConfig(&tls.Config{InsecureSkipVerify: true}))
	if err != nil {
		return err
	}
	stream, err := c.Dmesg(ctx, false, false)
	if err != nil {
		return err
	}
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		os.Stdout.Write(msg.Bytes)
	}
}

func init() {
	if len(os.Args) > 2 && os.Args[2] == "dmesg" {
		if err := dmesg(os.Args[1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(0)
	}
}
