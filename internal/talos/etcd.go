package talos

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"go.etcd.io/bbolt"
)

// EtcdSnapshot streams a consistent etcd snapshot (the bbolt database) from this
// control plane.
func (c *Client) EtcdSnapshot(ctx context.Context) (io.ReadCloser, error) {
	return c.Client.EtcdSnapshot(c.Context(ctx), &machine.EtcdSnapshotRequest{})
}

// EtcdRecoverUpload stages a snapshot on this control plane for a recovery bootstrap.
func (c *Client) EtcdRecoverUpload(ctx context.Context, snapshot io.Reader) error {
	_, err := c.Client.EtcdRecover(c.Context(ctx), snapshot)
	return err
}

// BootstrapRecover bootstraps etcd from the snapshot uploaded with EtcdRecoverUpload.
func (c *Client) BootstrapRecover(ctx context.Context) error {
	return c.Bootstrap(c.Context(ctx), &machine.BootstrapRequest{RecoverEtcd: true})
}

// ResetEphemeral wipes only the EPHEMERAL partition (etcd data, container state) and
// reboots; STATE with the machine config is kept, so the node comes back as a member
// waiting for etcd instead of dropping into maintenance mode.
func (c *Client) ResetEphemeral(ctx context.Context) error {
	return c.ResetGeneric(c.Context(ctx), &machine.ResetRequest{
		Graceful:               false,
		Reboot:                 true,
		SystemPartitionsToWipe: []*machine.ResetPartitionSpec{{Label: "EPHEMERAL", Wipe: true}},
	})
}

// VerifySnapshot opens an etcd snapshot read-only and counts its keys; a truncated or
// corrupt file fails here rather than at restore time.
func VerifySnapshot(path string) (keys int64, err error) {
	db, err := bbolt.Open(path, 0o400, &bbolt.Options{ReadOnly: true})
	if err != nil {
		return 0, fmt.Errorf("not a bbolt database: %w", err)
	}
	defer db.Close()
	err = db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("key"))
		if b == nil {
			return errors.New("no key bucket: not an etcd snapshot")
		}
		keys = int64(b.Stats().KeyN)
		return nil
	})
	return keys, err
}
