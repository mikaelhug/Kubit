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
	var addr, pxeIface, kubitURL string
	var system, pxe bool
	install := &cobra.Command{
		Use:   "install",
		Short: "Write the unit for `kubit serve` and start it now and at login (--pxe: the root PXE service instead)",
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
			if pxe {
				if os.Geteuid() != 0 {
					return fmt.Errorf("the PXE service binds ports 67/69: run this once with sudo (sudo %s service install --pxe --iface %s)", bin, pxeIface)
				}
				path, err := installPXEUnit(unit{Label: pxeLabel, Binary: bin, Home: home, Iface: pxeIface, KubitURL: kubitURL, Log: filepath.Join(home, "log", "pxe.log"), User: os.Getenv("SUDO_USER")})
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "installed %s\nkubit pxe answers on %s for %s; it is safe to leave running (members boot locally, enrollment gate applies)\n", path, pxeIface, kubitURL)
				return nil
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
	install.Flags().BoolVar(&pxe, "pxe", false, "install the always-on PXE server as a root service (needs sudo once)")
	install.Flags().StringVar(&pxeIface, "iface", "en0", "with --pxe: LAN interface to answer on")
	install.Flags().StringVar(&kubitURL, "kubit-url", "http://127.0.0.1:8080", "with --pxe: the daemon the PXE server asks about each MAC")
	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop the service and remove its unit; ~/.kubit is left untouched (--pxe: the PXE service)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if pxe {
				if runtime.GOOS == "darwin" {
					_ = run("launchctl", "bootout", "system/"+pxeLabel)
					err := os.Remove("/Library/LaunchDaemons/" + pxeLabel + ".plist")
					if err != nil && !os.IsNotExist(err) {
						return err
					}
					fmt.Fprintln(cmd.OutOrStdout(), "removed the PXE service")
					return nil
				}
				_ = run("systemctl", "disable", "--now", "kubit-pxe.service")
				_ = os.Remove("/etc/systemd/system/kubit-pxe.service")
				fmt.Fprintln(cmd.OutOrStdout(), "removed the PXE service")
				return nil
			}
			path, err := uninstallUnit(system)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", path)
			return nil
		},
	}
	uninstall.Flags().BoolVar(&system, "system", false, "Linux: the system unit")
	uninstall.Flags().BoolVar(&pxe, "pxe", false, "remove the PXE service")
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
	Iface, KubitURL                      string
}

const pxeLabel = "dev.kubit.pxe"

var launchdPXE = template.Must(template.New("pxe").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>{{.Label}}</string>
  <key>ProgramArguments</key>
  <array>
    <string>{{.Binary}}</string><string>pxe</string>
    <string>--iface</string><string>{{.Iface}}</string>
    <string>--kubit-url</string><string>{{.KubitURL}}</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict><key>KUBIT_HOME</key><string>{{.Home}}</string></dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>{{.Log}}</string>
  <key>StandardErrorPath</key><string>{{.Log}}</string>
</dict>
</plist>
`))

var systemdPXE = template.Must(template.New("pxe-unit").Parse(`[Unit]
Description=Kubit PXE server (proxyDHCP + TFTP + HTTP boot assets)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart={{.Binary}} pxe --iface {{.Iface}} --kubit-url {{.KubitURL}}
Environment=KUBIT_HOME={{.Home}}
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
`))

// installPXEUnit writes and starts the root PXE service (launchd system daemon on
// macOS, system unit on Linux). The PXE process reads Kubit's cache under KUBIT_HOME
// of the invoking user, so the log and cache stay with that user's install.
func installPXEUnit(u unit) (string, error) {
	if err := os.MkdirAll(filepath.Dir(u.Log), 0o755); err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		path := "/Library/LaunchDaemons/" + pxeLabel + ".plist"
		body, err := execTemplate(launchdPXE, u)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return "", err
		}
		_ = run("launchctl", "bootout", "system/"+pxeLabel)
		if err := run("launchctl", "bootstrap", "system", path); err != nil {
			return path, err
		}
		return path, run("launchctl", "kickstart", "-k", "system/"+pxeLabel)
	case "linux":
		path := "/etc/systemd/system/kubit-pxe.service"
		body, err := execTemplate(systemdPXE, u)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return "", err
		}
		if err := run("systemctl", "daemon-reload"); err != nil {
			return path, err
		}
		return path, run("systemctl", "enable", "--now", "kubit-pxe.service")
	}
	return "", fmt.Errorf("unsupported OS %s", runtime.GOOS)
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
