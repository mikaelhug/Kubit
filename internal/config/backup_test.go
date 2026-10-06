package config_test

import (
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/config"
)

const backupBase = "apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: lab}\nspec:\n  nodes:\n    - {hostname: cp, ip: 10.0.0.1, role: controlplane, installDisk: {path: /dev/sda}}\n    - {hostname: w, ip: 10.0.0.2, role: worker, installDisk: {path: /dev/sda}}\n"

func TestBackupWithoutAValidAgeRecipientIsRefused(t *testing.T) {
	if _, err := config.Parse([]byte(backupBase + "  backup: {schedule: \"0 */6 * * *\", s3: {bucket: b}}\n")); err == nil || !strings.Contains(err.Error(), "ageRecipients") {
		t.Errorf("a backup without recipients: %v", err)
	}
	if _, err := config.Parse([]byte(backupBase + "  backup: {schedule: \"0 */6 * * *\", s3: {bucket: b}, ageRecipients: [nope]}\n")); err == nil || !strings.Contains(err.Error(), "not an age public key") {
		t.Errorf("a bad recipient: %v", err)
	}
}
