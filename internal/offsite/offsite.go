// Package offsite copies Kubit's disaster-recovery material (sealed etcd snapshots and
// Kubit backups) to a second location, so losing the admin host together with the
// cluster does not lose the way back. Objects stay sealed; the master key travels
// separately (kubit key export).
package offsite

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Target is one destination; Type selects the implementation.
type Target struct {
	Type   string `json:"type"` // "" (off) | dir | s3
	Prefix string `json:"prefix"`
	// dir: any local or mounted path (SMB/NFS share, USB disk, rsync'ed folder).
	Dir string `json:"dir"`
	// s3: any S3-compatible endpoint (AWS, MinIO, Backblaze B2, Wasabi, Hetzner…).
	Endpoint  string `json:"endpoint"`
	Bucket    string `json:"bucket"`
	Region    string `json:"region"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"` // sealed at rest by the settings store
	Insecure  bool   `json:"insecure"`  // plain http endpoint (LAN MinIO)
	PathStyle bool   `json:"pathStyle"`
	// KeepBackups bounds the daily Kubit backups kept remotely; snapshots follow the
	// cluster's own retention.
	KeepBackups int `json:"keepBackups"`
}

func (t Target) Enabled() bool { return t.Type == "dir" || t.Type == "s3" }

func (t Target) String() string {
	switch t.Type {
	case "dir":
		return "dir " + t.Dir
	case "s3":
		return fmt.Sprintf("s3 %s/%s", t.Endpoint, t.Bucket)
	}
	return "off"
}

type Object struct {
	Key     string
	Size    int64
	ModTime time.Time
}

// Store is what both implementations provide; keys are slash-separated and relative
// to the target's prefix.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	List(ctx context.Context, prefix string) ([]Object, error)
	Delete(ctx context.Context, key string) error
}

func Open(t Target) (Store, error) {
	switch t.Type {
	case "dir":
		if t.Dir == "" {
			return nil, errors.New("off-site directory is empty")
		}
		return &dirStore{root: filepath.Join(t.Dir, filepath.FromSlash(t.Prefix))}, nil
	case "s3":
		if t.Endpoint == "" || t.Bucket == "" {
			return nil, errors.New("off-site S3 needs an endpoint and a bucket")
		}
		ep := strings.TrimPrefix(strings.TrimPrefix(t.Endpoint, "https://"), "http://")
		cl, err := minio.New(ep, &minio.Options{
			Creds:        credentials.NewStaticV4(t.AccessKey, t.SecretKey, ""),
			Secure:       !t.Insecure && !strings.HasPrefix(t.Endpoint, "http://"),
			Region:       t.Region,
			BucketLookup: lookup(t.PathStyle),
		})
		if err != nil {
			return nil, err
		}
		return &s3Store{cl: cl, bucket: t.Bucket, prefix: strings.Trim(t.Prefix, "/")}, nil
	}
	return nil, errors.New("off-site copies are off")
}

func lookup(pathStyle bool) minio.BucketLookupType {
	if pathStyle {
		return minio.BucketLookupPath
	}
	return minio.BucketLookupAuto
}

// Probe writes, reads back and deletes a small object: what "Test" in the settings
// does. It returns the round-trip time so a slow share is visible.
func Probe(ctx context.Context, st Store) (time.Duration, error) {
	start := time.Now()
	key := ".kubit-probe-" + start.UTC().Format("20060102T150405Z")
	body := []byte("kubit off-site probe " + start.String())
	if err := st.Put(ctx, key, strings.NewReader(string(body)), int64(len(body))); err != nil {
		return 0, fmt.Errorf("write: %w", err)
	}
	rc, err := st.Get(ctx, key)
	if err != nil {
		return 0, fmt.Errorf("read back: %w", err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || string(got) != string(body) {
		return 0, fmt.Errorf("read back: content mismatch")
	}
	if err := st.Delete(ctx, key); err != nil {
		return 0, fmt.Errorf("delete: %w", err)
	}
	return time.Since(start), nil
}

// PruneOldest deletes objects under prefix beyond keep, oldest first (by key, which
// Kubit names with a UTC timestamp).
func PruneOldest(ctx context.Context, st Store, prefix string, keep int) (int, error) {
	if keep <= 0 {
		return 0, nil
	}
	objs, err := st.List(ctx, prefix)
	if err != nil {
		return 0, err
	}
	sort.Slice(objs, func(i, j int) bool { return objs[i].Key > objs[j].Key })
	n := 0
	for i := keep; i < len(objs); i++ {
		if err := st.Delete(ctx, objs[i].Key); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// ─── directory ───────────────────────────────────────────────────────────────

type dirStore struct{ root string }

func (d *dirStore) path(key string) string { return filepath.Join(d.root, filepath.FromSlash(key)) }

func (d *dirStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	p := d.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	// Write beside, then rename: a half-copied file must never look like a backup.
	tmp := p + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, p)
}

func (d *dirStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	return os.Open(d.path(key))
}

func (d *dirStore) List(_ context.Context, prefix string) ([]Object, error) {
	var out []Object
	root := d.path(prefix)
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == root {
				return nil
			}
			return err
		}
		if info.IsDir() || strings.HasSuffix(p, ".part") {
			return nil
		}
		rel, _ := filepath.Rel(d.root, p)
		out = append(out, Object{Key: filepath.ToSlash(rel), Size: info.Size(), ModTime: info.ModTime()})
		return nil
	})
	return out, err
}

func (d *dirStore) Delete(_ context.Context, key string) error {
	err := os.Remove(d.path(key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ─── S3 ──────────────────────────────────────────────────────────────────────

type s3Store struct {
	cl     *minio.Client
	bucket string
	prefix string
}

func (s *s3Store) key(k string) string { return path.Join(s.prefix, k) }

func (s *s3Store) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	_, err := s.cl.PutObject(ctx, s.bucket, s.key(key), r, size, minio.PutObjectOptions{ContentType: "application/octet-stream"})
	return err
}

func (s *s3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.cl.GetObject(ctx, s.bucket, s.key(key), minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	// GetObject is lazy; touch the stat so a missing key fails here, not mid-read.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		return nil, err
	}
	return obj, nil
}

func (s *s3Store) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	p := s.key(prefix)
	if p != "" && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	for o := range s.cl.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: p, Recursive: true}) {
		if o.Err != nil {
			return nil, o.Err
		}
		rel := strings.TrimPrefix(o.Key, s.prefix)
		out = append(out, Object{Key: strings.TrimPrefix(rel, "/"), Size: o.Size, ModTime: o.LastModified})
	}
	return out, nil
}

func (s *s3Store) Delete(ctx context.Context, key string) error {
	return s.cl.RemoveObject(ctx, s.bucket, s.key(key), minio.RemoveObjectOptions{})
}
