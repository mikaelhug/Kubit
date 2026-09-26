package talos

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"go.etcd.io/bbolt"
)

func (c *Client) EtcdSnapshot(ctx context.Context) (io.ReadCloser, error) {
	return c.Client.EtcdSnapshot(c.Context(ctx), &machine.EtcdSnapshotRequest{})
}

func (c *Client) EtcdRecoverUpload(ctx context.Context, snapshot io.Reader) error {
	_, err := c.Client.EtcdRecover(c.Context(ctx), snapshot)
	return err
}

func (c *Client) BootstrapRecover(ctx context.Context) error {
	return c.Bootstrap(c.Context(ctx), &machine.BootstrapRequest{RecoverEtcd: true})
}

func (c *Client) ResetEphemeral(ctx context.Context) error {
	return c.ResetGeneric(c.Context(ctx), &machine.ResetRequest{
		Graceful:               false,
		Reboot:                 true,
		SystemPartitionsToWipe: []*machine.ResetPartitionSpec{{Label: "EPHEMERAL", Wipe: true}},
	})
}

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
