package tofu

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Line struct {
	Phase   string `json:"-"`
	Level   string `json:"@level"`
	Message string `json:"@message"`
	Type    string `json:"type"`
	Change  *struct {
		Resource struct {
			Addr string `json:"addr"`
		} `json:"resource"`
		Action string `json:"action"`
	} `json:"change,omitempty"`
	Hook *struct {
		Resource struct {
			Addr string `json:"addr"`
		} `json:"resource"`
		Action         string `json:"action"`
		ElapsedSeconds int    `json:"elapsed_seconds"`
	} `json:"hook,omitempty"`
	Changes *struct {
		Add    int `json:"add"`
		Change int `json:"change"`
		Remove int `json:"remove"`
	} `json:"changes,omitempty"`
	Diagnostic *struct {
		Severity string `json:"severity"`
		Summary  string `json:"summary"`
		Detail   string `json:"detail"`
	} `json:"diagnostic,omitempty"`
}

type Summary struct {
	Add, Change, Remove int
}

func (s Summary) Empty() bool { return s.Add == 0 && s.Change == 0 && s.Remove == 0 }

func (s Summary) String() string {
	return fmt.Sprintf("%d to add, %d to change, %d to destroy", s.Add, s.Change, s.Remove)
}

type Runner struct {
	Bin          string
	Dir          string
	StatePath    string
	Passphrase   string
	Env          []string
	Log          func(Line)
	lastWarnings []string
}

func (r *Runner) Warnings() []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range r.lastWarnings {
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

func (r *Runner) Init(ctx context.Context) error {
	args := []string{"init", "-input=false", "-no-color", "-reconfigure"}
	if r.StatePath != "" {
		if err := os.MkdirAll(filepath.Dir(r.StatePath), 0o755); err != nil {
			return err
		}
		args = append(args, "-backend-config=path="+r.StatePath)
	}
	_, err := r.run(ctx, args...)
	return err
}

func (r *Runner) Plan(ctx context.Context) (Summary, error) {
	return r.run(ctx, "plan", "-input=false", "-json", "-out=plan.tfplan")
}

func (r *Runner) Apply(ctx context.Context) (Summary, error) {
	return r.run(ctx, "apply", "-input=false", "-json", "plan.tfplan")
}

func (r *Runner) Outputs(ctx context.Context) (map[string]string, error) {
	out, err := r.output(ctx, "output", "-json")
	if err != nil {
		return nil, err
	}
	var raw map[string]struct {
		Value any `json:"value"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	res := make(map[string]string, len(raw))
	for k, v := range raw {
		res[k] = fmt.Sprint(v.Value)
	}
	return res, nil
}

func (r *Runner) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	cmd.Dir = r.Dir
	cmd.Env = r.env()
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = time.Minute
	return cmd
}

func (r *Runner) output(ctx context.Context, args ...string) ([]byte, error) {
	out, err := r.command(ctx, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("tofu %s: %w", args[0], err)
	}
	return out, nil
}

func (r *Runner) env() []string {
	env := append(childEnv(), "TF_IN_AUTOMATION=1", "TF_INPUT=0")
	if r.Passphrase != "" {
		env = append(env, "TF_ENCRYPTION="+Encryption(r.Passphrase))
	}
	return append(env, r.Env...)
}

func Encryption(passphrase string) string {
	return fmt.Sprintf(`key_provider "pbkdf2" "kubit" {
  passphrase = %q
}
method "aes_gcm" "kubit" {
  keys = key_provider.pbkdf2.kubit
}
method "unencrypted" "migrate" {}
state {
  method = method.aes_gcm.kubit
  fallback {
    method = method.unencrypted.migrate
  }
}
plan {
  method = method.aes_gcm.kubit
}
`, passphrase)
}

var secretEnv = []string{"KUBIT_MASTER_KEY", "KUBIT_TOKEN", "TF_ENCRYPTION", "SOPS_AGE_KEY", "KUBIT_SMTP_PASSWORD", "TF_VAR_backup_access_key_id", "TF_VAR_backup_secret_access_key"}

func childEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(secretEnv, name) {
			out = append(out, kv)
		}
	}
	return out
}

func (r *Runner) run(ctx context.Context, args ...string) (Summary, error) {
	cmd := r.command(ctx, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Summary{}, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return Summary{}, err
	}
	var (
		sum   Summary
		diags []string
		plain strings.Builder
	)
	r.lastWarnings = nil
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		text := sc.Text()
		var l Line
		if err := json.Unmarshal([]byte(text), &l); err != nil {
			plain.WriteString(text + "\n")
			if r.Log != nil {
				r.Log(Line{Phase: args[0], Level: "info", Message: text, Type: "text"})
			}
			continue
		}
		l.Phase = args[0]
		if l.Changes != nil {
			sum = Summary{Add: l.Changes.Add, Change: l.Changes.Change, Remove: l.Changes.Remove}
		}
		if l.Diagnostic != nil && l.Diagnostic.Severity == "error" {
			diags = append(diags, strings.TrimSpace(l.Diagnostic.Summary+": "+l.Diagnostic.Detail))
		}
		if l.Diagnostic != nil && l.Diagnostic.Severity == "warning" {
			r.lastWarnings = append(r.lastWarnings, strings.TrimSpace(strings.TrimSpace(l.Diagnostic.Summary)+": "+strings.TrimSpace(l.Diagnostic.Detail)))
		}
		if r.Log != nil {
			r.Log(l)
		}
	}
	_, _ = io.Copy(io.Discard, stdout)
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(diags) > 0 {
			msg = strings.Join(diags, "; ")
		}
		if msg == "" {
			msg = strings.TrimSpace(plain.String())
		}
		return sum, fmt.Errorf("tofu %s: %w: %s", args[0], err, msg)
	}
	return sum, nil
}
