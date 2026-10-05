package config_test

import (
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/mikael/kubit/internal/config"
)

const backupBase = "apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: lab}\nspec:\n  nodes:\n    - {hostname: cp, ip: 10.0.0.1, role: controlplane, installDisk: {path: /dev/sda}}\n    - {hostname: w, ip: 10.0.0.2, role: worker, installDisk: {path: /dev/sda}}\n"

func TestBackupIsOffUntilDeclared(t *testing.T) {
	c, err := config.Parse([]byte(backupBase + "  backup: {etcd: {interval: 6h, keep: 28}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Spec.Backup.Enabled() {
		t.Error("the old etcd schedule must not turn backups on")
	}
	b, _ := c.Marshal()
	if strings.Contains(string(b), "backup") {
		t.Errorf("an undeclared backup must not be written:\n%s", b)
	}
	gen, err := config.Generate(c, nil, config.FixedInstaller("installer"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gen.Nodes["cp"]), "kubernetesTalosAPIAccess") {
		t.Error("no Talos API access without a backup")
	}
}

func TestDeclaredBackupValidatesAndOpensTalosAPIAccess(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	if _, err := config.Parse([]byte(backupBase + "  backup: {schedule: \"0 */6 * * *\"}\n")); err == nil || !strings.Contains(err.Error(), "bucket") || !strings.Contains(err.Error(), "ageRecipients") {
		t.Errorf("an incomplete backup: %v", err)
	}
	if _, err := config.Parse([]byte(backupBase + "  backup: {schedule: daily, s3: {bucket: b}, ageRecipients: [nope]}\n")); err == nil || !strings.Contains(err.Error(), "cron") || !strings.Contains(err.Error(), "not an age public key") {
		t.Errorf("bad schedule and recipient: %v", err)
	}
	c, err := config.Parse([]byte(backupBase + "  backup: {schedule: \"0 */6 * * *\", s3: {bucket: b, endpoint: \"https://s3.example.com\"}, ageRecipients: [" + id.Recipient().String() + "]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Spec.Backup.S3.Region != "us-east-1" {
		t.Errorf("region default %q", c.Spec.Backup.S3.Region)
	}
	gen, err := config.Generate(c, nil, config.FixedInstaller("installer"))
	if err != nil {
		t.Fatal(err)
	}
	cp, w := string(gen.Nodes["cp"]), string(gen.Nodes["w"])
	if !strings.Contains(cp, "kubernetesTalosAPIAccess") || !strings.Contains(cp, "os:etcd:backup") || !strings.Contains(cp, config.BackupNamespace) {
		t.Errorf("control plane lacks Talos API access:\n%s", cp)
	}
	if strings.Contains(w, "kubernetesTalosAPIAccess") {
		t.Error("workers need no Talos API access")
	}
}
