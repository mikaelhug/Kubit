package vfkit

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
)

type spec struct {
	Name    string `json:"name"`
	MAC     string `json:"mac"`
	CPUs    int    `json:"cpus"`
	MemMiB  int    `json:"memMiB"`
	DiskGiB int    `json:"diskGiB"`
	DataGiB int    `json:"dataGiB,omitempty"`
	Boot    string `json:"boot"`
	ISO     string `json:"iso,omitempty"`
	Wipe    bool   `json:"wipe,omitempty"`
	Run     bool   `json:"run"`
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func checkName(name string) error {
	if !validName.MatchString(name) || name == bootDir {
		return fmt.Errorf("%q is not a usable VM name", name)
	}
	return nil
}

func (h *Host) readSpec(name string) (*spec, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(h.file(name, "spec.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no VM named %s", name)
		}
		return nil, err
	}
	var s spec
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return &s, nil
}

func (h *Host) writeSpec(s *spec) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(h.file(s.Name, "spec.json"), b)
}

func (h *Host) specs() ([]*spec, error) {
	entries, err := os.ReadDir(h.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*spec
	for _, e := range entries {
		if !e.IsDir() || e.Name() == bootDir {
			continue
		}
		if s, err := h.readSpec(e.Name()); err == nil {
			out = append(out, s)
		}
	}
	return out, nil
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".part"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
