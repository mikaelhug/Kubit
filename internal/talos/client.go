// Package talos wraps the Talos machine API for the two ways Kubit reaches a node:
// insecure maintenance mode before a config is applied, and mTLS with the cluster's
// talosconfig afterwards.
package talos

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const Port = "50000"

type Client struct {
	*client.Client
	IP string
}

// DialMaintenance connects without verifying the server certificate; only a node in
// maintenance mode accepts such a connection.
func DialMaintenance(ctx context.Context, ip string) (*Client, error) {
	c, err := client.New(ctx,
		client.WithEndpoints(ip),
		client.WithTLSConfig(&tls.Config{InsecureSkipVerify: true}), //nolint:gosec // maintenance API has no CA yet
	)
	if err != nil {
		return nil, err
	}
	return &Client{Client: c, IP: ip}, nil
}

// Dial connects with the cluster's talosconfig, addressing the node directly.
func Dial(ctx context.Context, ip string, talosconfig []byte) (*Client, error) {
	cfg, err := clientconfig.FromBytes(talosconfig)
	if err != nil {
		return nil, fmt.Errorf("talosconfig: %w", err)
	}
	c, err := client.New(ctx, client.WithConfig(cfg), client.WithEndpoints(ip))
	if err != nil {
		return nil, err
	}
	return &Client{Client: c, IP: ip}, nil
}

// Context returns a ctx that targets this node when the call is proxied via apid.
func (c *Client) Context(ctx context.Context) context.Context {
	return client.WithNode(ctx, c.IP)
}

// PortOpen reports whether the Talos API port accepts TCP connections.
func PortOpen(ip string, timeout time.Duration) bool { return PortErr(ip, timeout) == nil }

// PortErr is PortOpen with the dial error, so callers can tell a closed port from a
// host they cannot route to.
func PortErr(ip string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip, Port), timeout)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

func ShortGRPC(err error) error {
	var gs interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &gs) {
		return err
	}
	st := gs.GRPCStatus()
	prefix := err.Error()
	if inner, ok := gs.(error); ok {
		prefix = strings.TrimSuffix(prefix, inner.Error())
	}
	msg := st.Message()
	if rest, ok := strings.CutPrefix(msg, "connection error: desc = "); ok {
		if unq, err := strconv.Unquote(rest); err == nil {
			msg = unq
		}
	}
	return fmt.Errorf("%s%s (%s)", prefix, msg, st.Code())
}

func HTTPStatus(err error) int {
	switch status.Code(err) {
	case codes.Unavailable:
		return 502
	case codes.DeadlineExceeded:
		return 504
	default:
		return 500
	}
}
