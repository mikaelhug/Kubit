package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWALIsSetAsideForARestore(t *testing.T) {
	home := t.TempDir()
	db := filepath.Join(home, dbFile)
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}
	write(db, "old db")
	write(db+"-wal", "old wal")
	write(db+"-shm", "old shm")

	done, err := setWALAside(home)
	if err != nil {
		t.Fatal(err)
	}
	if read(db+"-wal") != "<missing>" || read(db+"-shm") != "<missing>" {
		t.Fatal("the WAL must be gone before the database is written")
	}
	done(false, false)
	if read(db+"-wal") != "old wal" || read(db+"-shm") != "old shm" {
		t.Error("a restore that failed before touching the database puts the WAL back")
	}

	done, _ = setWALAside(home)
	done(false, true)
	if read(db+"-wal") != "old wal" || read(db+"-shm") != "old shm" {
		t.Error("a successful restore that did not write the database keeps its WAL")
	}

	done, _ = setWALAside(home)
	write(db, "restored db")
	done(true, true)
	if read(db+"-wal") != "<missing>" || read(db+"-wal.pre-restore") != "<missing>" {
		t.Error("a successful restore of the database drops the old WAL")
	}

	write(db+"-wal", "old wal")
	done, _ = setWALAside(home)
	write(db, "half written")
	done(true, false)
	if read(db+"-wal") != "<missing>" || read(db+"-wal.pre-restore") != "old wal" {
		t.Error("an old WAL never goes back onto a changed database; it is kept aside")
	}
}
