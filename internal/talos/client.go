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
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/status"
)

const Port = "50000"

type Client struct {
	*client.Client
	IP string
}

func DialMaintenance(ctx context.Context, ip string) (*Client, error) {
	c, err := client.New(ctx,
		client.WithEndpoints(ip),
		client.WithTLSConfig(&tls.Config{InsecureSkipVerify: true}), //nolint:gosec
	)
	if err != nil {
		return nil, err
	}
	return &Client{Client: c, IP: ip}, nil
}

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

func (c *Client) TransientFailure() bool {
	return c.Conn().GetState() == connectivity.TransientFailure
}

func (c *Client) nodeContext(ctx context.Context) context.Context {
	return client.WithNode(ctx, c.IP)
}

func PortOpen(ctx context.Context, ip string, timeout time.Duration) bool {
	return PortErr(ctx, ip, timeout) == nil
}

func PortErr(ctx context.Context, ip string, timeout time.Duration) error {
	return TCPErr(ctx, ip, Port, timeout)
}

func TCPErr(ctx context.Context, host, port string, timeout time.Duration) error {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
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
