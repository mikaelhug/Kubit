package boot

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
	"github.com/diskfs/go-diskfs/partition/mbr"
)

// Entry is one file placed both on the EFI system partition and in the ISO tree.
type Entry struct {
	Path string
	Src  string
	Data []byte
}

// BuildISO writes a UEFI-bootable CD at out: an El Torito EFI entry pointing at a
// FAT image that holds the entries, plus the same entries in the ISO9660 tree.
// The result is padded to a multiple of 2048 bytes, as IDE-R requires.
func BuildISO(out, label string, entries []Entry) error {
	tmp, err := os.MkdirTemp(filepath.Dir(out), ".iso-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	esp := filepath.Join(tmp, "efiboot.img")
	if err := buildFAT(esp, entries); err != nil {
		return fmt.Errorf("efi image: %w", err)
	}
	ws := filepath.Join(tmp, "ws")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		return err
	}
	f, err := os.Create(out + ".part")
	if err != nil {
		return err
	}
	fs, err := iso9660.Create(file.New(f, false), 0, 0, 2048, ws)
	if err != nil {
		f.Close()
		return err
	}
	put := func(path string, src string, data []byte) error {
		if dir := filepath.Dir(path); dir != "/" && dir != "." {
			if err := fs.Mkdir(dir); err != nil {
				return err
			}
		}
		w, err := fs.OpenFile(path, os.O_CREATE|os.O_RDWR)
		if err != nil {
			return err
		}
		defer w.Close()
		if src != "" {
			r, err := os.Open(src)
			if err != nil {
				return err
			}
			defer r.Close()
			_, err = io.Copy(w, r)
			return err
		}
		_, err = w.Write(data)
		return err
	}
	if err := put("/EFIBOOT.IMG", esp, nil); err != nil {
		f.Close()
		return err
	}
	for _, e := range entries {
		if err := put(e.Path, e.Src, e.Data); err != nil {
			f.Close()
			return fmt.Errorf("%s: %w", e.Path, err)
		}
	}
	if err := fs.Finalize(iso9660.FinalizeOptions{
		RockRidge: true, VolumeIdentifier: label,
		ElTorito: &iso9660.ElTorito{BootCatalog: "/BOOT.CAT", Entries: []*iso9660.ElToritoEntry{
			{Platform: iso9660.EFI, Emulation: iso9660.NoEmulation, BootFile: "/EFIBOOT.IMG", SystemType: mbr.Fat32LBA},
		}},
	}); err != nil {
		f.Close()
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if pad := (2048 - st.Size()%2048) % 2048; pad > 0 {
		if _, err := f.Write(make([]byte, pad)); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(out+".part", out)
}

func buildFAT(path string, entries []Entry) error {
	var total int64
	for _, e := range entries {
		if e.Src != "" {
			st, err := os.Stat(e.Src)
			if err != nil {
				return err
			}
			total += st.Size()
		} else {
			total += int64(len(e.Data))
		}
	}
	size := total + 32<<20
	size += (512 - size%512) % 512
	b, err := file.CreateFromPath(path, size)
	if err != nil {
		return err
	}
	fs, err := fat32.Create(b, size, 0, 512, "KUBITBOOT", true)
	if err != nil {
		return err
	}
	for _, e := range entries {
		p := "/" + filepath.ToSlash(e.Path)
		p = filepath.Clean(p)
		if dir := filepath.Dir(p); dir != "/" {
			if err := fs.Mkdir(dir); err != nil {
				return err
			}
		}
		w, err := fs.OpenFile(p, os.O_CREATE|os.O_RDWR)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		if e.Src != "" {
			r, err := os.Open(e.Src)
			if err != nil {
				w.Close()
				return err
			}
			_, err = io.Copy(w, r)
			r.Close()
			if err != nil {
				w.Close()
				return err
			}
		} else if _, err := w.Write(e.Data); err != nil {
			w.Close()
			return err
		}
		w.Close()
	}
	return fs.Close()
}
