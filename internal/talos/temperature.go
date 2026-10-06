package talos

import (
	"context"
	"errors"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/api/machine"
)

type Sensor string

const (
	SensorCPU  Sensor = "cpu"
	SensorDisk Sensor = "disk"
)

type Temperature struct {
	Sensor   Sensor  `json:"sensor"`
	Chip     string  `json:"chip"`
	Celsius  float64 `json:"celsius"`
	High     float64 `json:"high,omitempty"`
	Critical float64 `json:"critical,omitempty"`
}

var sensorOf = map[string]Sensor{
	"coretemp":    SensorCPU,
	"k10temp":     SensorCPU,
	"zenpower":    SensorCPU,
	"cpu_thermal": SensorCPU,
	"soc_thermal": SensorCPU,
	"nvme":        SensorDisk,
	"drivetemp":   SensorDisk,
}

const hwmonRoot = "/sys/class/hwmon"

func (c *Client) Temperatures(ctx context.Context) ([]Temperature, error) {
	chips, err := c.list(ctx, hwmonRoot)
	if err != nil {
		return nil, err
	}
	var out []Temperature
	for _, dir := range chips {
		name, err := c.readText(ctx, path.Join(dir, "name"))
		sensor, ok := sensorOf[name]
		if err != nil || !ok {
			continue
		}
		t := Temperature{Sensor: sensor, Chip: name}
		if t.Celsius, err = c.readMilli(ctx, path.Join(dir, "temp1_input")); err != nil {
			continue
		}
		t.High, _ = c.readMilli(ctx, path.Join(dir, "temp1_max"))
		t.Critical, _ = c.readMilli(ctx, path.Join(dir, "temp1_crit"))
		out = append(out, t)
	}
	slices.SortStableFunc(out, func(a, b Temperature) int { return strings.Compare(string(a.Sensor), string(b.Sensor)) })
	return out, nil
}

func (c *Client) list(ctx context.Context, root string) ([]string, error) {
	stream, err := c.LS(ctx, &machine.ListRequest{Root: root, Recurse: true, RecursionDepth: 1})
	if err != nil {
		return nil, err
	}
	var out []string
	for {
		e, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if e.Name != root && e.Error == "" {
			out = append(out, e.Name)
		}
	}
}

func (c *Client) readText(ctx context.Context, p string) (string, error) {
	rc, err := c.Read(ctx, p)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	return strings.TrimSpace(string(b)), err
}

func (c *Client) readMilli(ctx context.Context, p string) (float64, error) {
	s, err := c.readText(ctx, p)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseFloat(s, 64)
	return v / 1000, err
}
