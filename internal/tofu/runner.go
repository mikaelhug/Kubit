package tofu

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Line is one record of OpenTofu's machine-readable UI (-json).
type Line struct {
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
		Add       int    `json:"add"`
		Change    int    `json:"change"`
		Remove    int    `json:"remove"`
		Operation string `json:"operation"`
	} `json:"changes,omitempty"`
	Diagnostic *struct {
		Severity string `json:"severity"`
		Summary  string `json:"summary"`
		Detail   string `json:"detail"`
	} `json:"diagnostic,omitempty"`
}

// Summary is the add/change/remove count of a plan or apply.
type Summary struct {
	Add, Change, Remove int
}

func (s Summary) Empty() bool { return s.Add == 0 && s.Change == 0 && s.Remove == 0 }

func (s Summary) String() string {
	return fmt.Sprintf("%d to add, %d to change, %d to destroy", s.Add, s.Change, s.Remove)
}

type Runner struct {
	Bin string
	Dir string
	// Log receives every -json line; nil is allowed.
	Log func(Line)
}

func (r *Runner) Init(ctx context.Context) error {
	_, err := r.run(ctx, "init", "-input=false", "-no-color")
	return err
}

// Plan writes plan.tfplan and returns its change counts.
func (r *Runner) Plan(ctx context.Context) (Summary, error) {
	return r.run(ctx, "plan", "-input=false", "-json", "-out=plan.tfplan")
}

// Apply executes the last Plan.
func (r *Runner) Apply(ctx context.Context) (Summary, error) {
	return r.run(ctx, "apply", "-input=false", "-json", "plan.tfplan")
}

// Outputs returns root module outputs (sensitive values included).
func (r *Runner) Outputs(ctx context.Context) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, r.Bin, "output", "-json")
	cmd.Dir = r.Dir
	cmd.Env = r.env()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tofu output: %w", err)
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

func (r *Runner) env() []string {
	return append(os.Environ(), "TF_IN_AUTOMATION=1", "TF_INPUT=0")
}

func (r *Runner) run(ctx context.Context, args ...string) (Summary, error) {
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	cmd.Dir = r.Dir
	cmd.Env = r.env()
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
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		text := sc.Text()
		var l Line
		if err := json.Unmarshal([]byte(text), &l); err != nil {
			plain.WriteString(text + "\n")
			if r.Log != nil {
				r.Log(Line{Level: "info", Message: text, Type: "text"})
			}
			continue
		}
		if l.Changes != nil {
			sum = Summary{Add: l.Changes.Add, Change: l.Changes.Change, Remove: l.Changes.Remove}
		}
		if l.Diagnostic != nil && l.Diagnostic.Severity == "error" {
			diags = append(diags, strings.TrimSpace(l.Diagnostic.Summary+": "+l.Diagnostic.Detail))
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
