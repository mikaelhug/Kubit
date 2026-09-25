package store

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Backup writes an encrypted tar.gz of the Kubit home: the database and every cluster
// directory (kubeconfig, talosconfig, infra roots and their state). The archive is
// sealed with the master key, so it is only useful on a machine that has the same key
// (the Keychain entry, or KUBIT_MASTER_KEY exported with `kubit key export`).
func Backup(home string, crypto *Crypto, w io.Writer) error {
	pr, pw := io.Pipe()
	errc := make(chan error, 1)
	go func() {
		gz := gzip.NewWriter(pw)
		tw := tar.NewWriter(gz)
		err := filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(home, path)
			if rel == "." || strings.HasPrefix(rel, "bin") || strings.HasPrefix(rel, "cache") || rel == "vms" || strings.HasPrefix(rel, "vms/") || strings.Contains(rel, ".terraform/") || strings.HasSuffix(rel, ".part") || strings.HasSuffix(rel, "-wal") || strings.HasSuffix(rel, "-shm") {
				if info.IsDir() && rel != "." && (rel == "bin" || rel == "cache" || rel == "vms" || strings.HasSuffix(rel, ".terraform")) {
					return filepath.SkipDir
				}
				return nil
			}
			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			hdr.Name = rel
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				_, err = io.Copy(tw, f)
				f.Close()
				return err
			}
			return nil
		})
		if err == nil {
			err = tw.Close()
		}
		if err == nil {
			err = gz.Close()
		}
		pw.CloseWithError(err)
		errc <- err
	}()
	data, err := io.ReadAll(pr)
	if err != nil {
		return err
	}
	if err := <-errc; err != nil {
		return err
	}
	sealed, err := crypto.Seal(data)
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(backupMagic)); err != nil {
		return err
	}
	_, err = w.Write(sealed)
	return err
}

const backupMagic = "KUBITBAK1\n"

// Restore unpacks a Backup archive into home, which must be empty unless force is set.
// Callers checkpoint the database (Store.Checkpoint) before Backup so the .db file is
// complete without its -wal.
func Restore(home string, crypto *Crypto, r io.Reader, force bool) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(raw), backupMagic) {
		return fmt.Errorf("not a Kubit backup")
	}
	data, err := crypto.Open(raw[len(backupMagic):])
	if err != nil {
		return fmt.Errorf("cannot decrypt: the master key differs from the one that made this backup: %w", err)
	}
	if entries, _ := os.ReadDir(home); len(entries) > 0 && !force {
		var names []string
		for _, e := range entries {
			if e.Name() != "bin" && e.Name() != "cache" && e.Name() != "vms" {
				names = append(names, e.Name())
			}
		}
		if len(names) > 0 {
			return fmt.Errorf("%s is not empty (%s); pass --force to overwrite", home, strings.Join(names, ", "))
		}
	}
	gz, err := gzip.NewReader(strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(home, filepath.Clean(hdr.Name))
		if !strings.HasPrefix(target, filepath.Clean(home)+string(os.PathSeparator)) {
			return fmt.Errorf("refusing path outside home: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777|0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
}
