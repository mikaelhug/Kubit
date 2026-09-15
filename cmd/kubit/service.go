package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
)

// The daemon is optional: a cluster runs without it. Installing it as a user service
// only adds the observer features (alerts, scheduled snapshots, watcher history).
const serviceLabel = "dev.kubit.serve"

func serviceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "service", Short: "Run the daemon as a user service (launchd on macOS, systemd --user on Linux)"}
	var addr string
	var system bool
	install := &cobra.Command{
		Use:   "install",
		Short: "Write the unit for `kubit serve` and start it now and at login",
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := homeDir()
			if err != nil {
				return err
			}
			bin, err := os.Executable()
			if err != nil {
				return err
			}
			if bin, err = filepath.EvalSymlinks(bin); err != nil {
				return err
			}
			u := unit{Label: serviceLabel, Binary: bin, Addr: addr, Home: home, Log: filepath.Join(home, "log", "serve.log"), User: os.Getenv("USER")}
			if err := os.MkdirAll(filepath.Dir(u.Log), 0o700); err != nil {
				return err
			}
			path, err := installUnit(u, system)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "installed %s\n", path)
			fmt.Fprintf(cmd.OutOrStdout(), "kubit serve runs at http://%s; log: %s\n", addr, u.Log)
			if runtime.GOOS == "linux" && !system {
				fmt.Fprintln(cmd.OutOrStdout(), "to keep it running while logged out: sudo loginctl enable-linger "+u.User)
			}
			return nil
		},
	}
	install.Flags().StringVar(&addr, "addr", "127.0.0.1:8080", "listen address for the service")
	install.Flags().BoolVar(&system, "system", false, "Linux: install a system unit in /etc/systemd/system (run as root; uses KUBIT_HOME of the invoking user)")
	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop the service and remove its unit; ~/.kubit is left untouched",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := uninstallUnit(system)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", path)
			return nil
		},
	}
	uninstall.Flags().BoolVar(&system, "system", false, "Linux: the system unit")
	status := &cobra.Command{
		Use:   "status",
		Short: "Show whether the service is loaded and running",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := serviceStatus(system)
			fmt.Fprint(cmd.OutOrStdout(), out)
			return err
		},
	}
	status.Flags().BoolVar(&system, "system", false, "Linux: the system unit")
	cmd.AddCommand(install, uninstall, status)
	return cmd
}

type unit struct {
	Label, Binary, Addr, Home, Log, User string
}

var launchdPlist = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>{{.Label}}</string>
  <key>ProgramArguments</key>
  <array>
    <string>{{.Binary}}</string>
    <string>serve</string>
    <string>--addr</string>
    <string>{{.Addr}}</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>KUBIT_HOME</key><string>{{.Home}}</string>
    <key>KUBIT_SERVICE</key><string>1</string>
    <key>PATH</key><string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin</string>
  </dict>
  <key>WorkingDirectory</key><string>{{.Home}}</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>{{.Log}}</string>
  <key>StandardErrorPath</key><string>{{.Log}}</string>
</dict>
</plist>
`))

var systemdUnit = template.Must(template.New("unit").Parse(`[Unit]
Description=Kubit daemon (Talos cluster console and watcher)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart={{.Binary}} serve --addr {{.Addr}}
Environment=KUBIT_HOME={{.Home}}
Environment=KUBIT_SERVICE=1
WorkingDirectory={{.Home}}
Restart=on-failure
RestartSec=3
{{if .User}}User={{.User}}
{{end}}
[Install]
WantedBy={{if .User}}multi-user.target{{else}}default.target{{end}}
`))

// renderUnit produces the platform's unit file; system selects the systemd system unit.
func renderUnit(goos string, u unit, system bool) (string, error) {
	var b strings.Builder
	switch goos {
	case "darwin":
		return execTemplate(launchdPlist, u)
	case "linux":
		if !system {
			u.User = "" // a --user unit runs as the session user already
		}
		if err := systemdUnit.Execute(&b, u); err != nil {
			return "", err
		}
		return b.String(), nil
	}
	return "", fmt.Errorf("no service manager support on %s; run `kubit serve` under your own supervisor", goos)
}

func execTemplate(t *template.Template, v any) (string, error) {
	var b strings.Builder
	if err := t.Execute(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}

func unitPath(system bool) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, "Library", "LaunchAgents", serviceLabel+".plist"), nil
	case "linux":
		if system {
			return "/etc/systemd/system/kubit.service", nil
		}
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, ".config", "systemd", "user", "kubit.service"), nil
	}
	return "", fmt.Errorf("unsupported OS %s", runtime.GOOS)
}

func installUnit(u unit, system bool) (string, error) {
	body, err := renderUnit(runtime.GOOS, u, system)
	if err != nil {
		return "", err
	}
	path, err := unitPath(system)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		domain := fmt.Sprintf("gui/%d", os.Getuid())
		_ = run("launchctl", "bootout", domain+"/"+serviceLabel) // reinstall: ignore "not loaded"
		if err := run("launchctl", "bootstrap", domain, path); err != nil {
			return path, err
		}
		return path, run("launchctl", "kickstart", "-k", domain+"/"+serviceLabel)
	case "linux":
		if system {
			if err := run("systemctl", "daemon-reload"); err != nil {
				return path, err
			}
			return path, run("systemctl", "enable", "--now", "kubit.service")
		}
		if err := run("systemctl", "--user", "daemon-reload"); err != nil {
			return path, err
		}
		return path, run("systemctl", "--user", "enable", "--now", "kubit.service")
	}
	return path, nil
}

func uninstallUnit(system bool) (string, error) {
	path, err := unitPath(system)
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		_ = run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), serviceLabel))
	case "linux":
		if system {
			_ = run("systemctl", "disable", "--now", "kubit.service")
		} else {
			_ = run("systemctl", "--user", "disable", "--now", "kubit.service")
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return path, err
	}
	return path, nil
}

func serviceStatus(system bool) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), serviceLabel)).CombinedOutput()
		if err != nil {
			return "not installed (launchctl: " + strings.TrimSpace(string(out)) + ")\n", nil
		}
		var lines []string
		for _, l := range strings.Split(string(out), "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "state =") || strings.HasPrefix(l, "pid =") || strings.HasPrefix(l, "path =") || strings.HasPrefix(l, "last exit code =") {
				lines = append(lines, l)
			}
		}
		return strings.Join(lines, "\n") + "\n", nil
	case "linux":
		args := []string{"status", "kubit.service", "--no-pager"}
		if !system {
			args = append([]string{"--user"}, args...)
		}
		out, _ := exec.Command("systemctl", args...).CombinedOutput()
		return string(out), nil
	}
	return "", fmt.Errorf("unsupported OS %s", runtime.GOOS)
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
