// talosver: minimal maintenance-mode probe used to gate the VM harness.
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/client"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := client.New(ctx,
		client.WithEndpoints(os.Args[1]),
		client.WithTLSConfig(&tls.Config{InsecureSkipVerify: true}),
	)
	if err != nil {
		panic(err)
	}
	v, err := c.Version(ctx)
	if err != nil {
		panic(err)
	}
	for _, m := range v.Messages {
		fmt.Printf("tag=%s arch=%s platform=%s mode=%s\n",
			m.Version.Tag, m.Version.Arch, m.Platform.Name, m.Platform.Mode)
	}
}
