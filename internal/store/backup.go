package store

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const dbFile = "kubit.db"

func snapshotDB(home string) (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "kubit-backup-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.RemoveAll(dir) }
	db, err := sql.Open("sqlite", filepath.Join(home, dbFile)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		cleanup()
		return "", nil, err
	}
	defer db.Close()
	path = filepath.Join(dir, dbFile)
	if _, err := db.Exec(`VACUUM INTO ?`, path); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

func Backup(home string, crypto *Crypto, w io.Writer) error {
	var snapshot string
	if _, err := os.Stat(filepath.Join(home, dbFile)); err == nil {
		path, cleanup, err := snapshotDB(home)
		if err != nil {
			return err
		}
		defer cleanup()
		snapshot = path
	}
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
			if rel == "." || rel == lockFile || strings.HasPrefix(rel, "bin") || strings.HasPrefix(rel, "cache") || rel == "vms" || strings.HasPrefix(rel, "vms/") || strings.Contains(rel, ".terraform/") || strings.HasSuffix(rel, ".part") || strings.HasSuffix(rel, "-wal") || strings.HasSuffix(rel, "-shm") {
				if info.IsDir() && rel != "." && (rel == "bin" || rel == "cache" || rel == "vms" || strings.HasSuffix(rel, ".terraform")) {
					return filepath.SkipDir
				}
				return nil
			}
			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			if rel == dbFile && snapshot != "" {
				snap, err := os.Stat(snapshot)
				if err != nil {
					return err
				}
				hdr.Size = snap.Size()
				path = snapshot
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

func Restore(home string, crypto *Crypto, r io.Reader, force bool) (written []string, err error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(string(raw), backupMagic) {
		return nil, fmt.Errorf("not a Kubit backup")
	}
	data, err := crypto.Open(raw[len(backupMagic):])
	if err != nil {
		return nil, fmt.Errorf("cannot decrypt: the master key differs from the one that made this backup: %w", err)
	}
	if entries, _ := os.ReadDir(home); len(entries) > 0 && !force {
		var names []string
		for _, e := range entries {
			switch e.Name() {
			case "bin", "cache", "vms", lockFile:
			default:
				names = append(names, e.Name())
			}
		}
		if len(names) > 0 {
			return nil, fmt.Errorf("%s is not empty (%s); pass --force to overwrite", home, strings.Join(names, ", "))
		}
	}
	gz, err := gzip.NewReader(strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return written, nil
		}
		if err != nil {
			return written, err
		}
		rel := filepath.Clean(hdr.Name)
		if rel == lockFile {
			continue
		}
		target := filepath.Join(home, rel)
		if !strings.HasPrefix(target, filepath.Clean(home)+string(os.PathSeparator)) {
			return written, fmt.Errorf("refusing path outside home: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return written, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return written, err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777|0o600)
			if err != nil {
				return written, err
			}
			written = append(written, rel)
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return written, err
			}
			f.Close()
		}
	}
}
