package boot

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
)

func TestBuildISO(t *testing.T) {
	dir := t.TempDir()
	kernel := filepath.Join(dir, "linux")
	if err := os.WriteFile(kernel, bytes.Repeat([]byte{0xAB}, 300000), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "test.iso")
	err := BuildISO(out, "KUBIT", []Entry{
		{Path: "/EFI/BOOT/BOOTX64.EFI", Data: []byte("MZ-fake")},
		{Path: "/linux", Src: kernel},
		{Path: "/loader/entries/debian.conf", Data: []byte("title x\nlinux /linux\noptions auto=true url=http://10.0.0.1:8069/labhost/aa/preseed\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size()%2048 != 0 {
		t.Errorf("size %d is not a multiple of 2048", st.Size())
	}
	b, err := file.OpenFromPath(out, true)
	if err != nil {
		t.Fatal(err)
	}
	iso, err := iso9660.Read(b, st.Size(), 0, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/EFI/BOOT/BOOTX64.EFI", "/linux", "/loader/entries/debian.conf", "/EFIBOOT.IMG", "/BOOT.CAT"} {
		if _, err := iso.OpenFile(p, os.O_RDONLY); err != nil {
			t.Errorf("%s missing from the ISO: %v", p, err)
		}
	}
	raw, _ := os.ReadFile(out)
	if !bytes.Contains(raw[17*2048:18*2048], []byte("EL TORITO SPECIFICATION")) {
		t.Error("no El Torito boot record at sector 17")
	}
	img, err := iso.OpenFile("/EFIBOOT.IMG", os.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "esp.img")
	w, _ := os.Create(tmp)
	n, _ := w.ReadFrom(img)
	w.Close()
	eb, _ := file.OpenFromPath(tmp, true)
	esp, err := fat32.Read(eb, n, 0, 512)
	if err != nil {
		t.Fatalf("esp is not FAT32: %v", err)
	}
	f, err := esp.OpenFile("/loader/entries/debian.conf", os.O_RDONLY)
	if err != nil {
		t.Fatalf("entry missing from the ESP: %v", err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(f)
	if !bytes.Contains(buf.Bytes(), []byte("url=http://10.0.0.1:8069/labhost/aa/preseed")) {
		t.Errorf("entry content: %q", buf.String())
	}
	k, _ := esp.OpenFile("/linux", os.O_RDONLY)
	buf.Reset()
	buf.ReadFrom(k)
	if buf.Len() != 300000 {
		t.Errorf("kernel on the ESP is %d bytes", buf.Len())
	}
}
