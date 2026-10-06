package repo

import (
	"bufio"
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type GitChange struct {
	Status string `json:"status"`
	Path   string `json:"path"`
}

type GitState struct {
	Repo     bool        `json:"repo"`
	Branch   string      `json:"branch,omitempty"`
	Upstream string      `json:"upstream,omitempty"`
	Ahead    int         `json:"ahead"`
	Changes  []GitChange `json:"changes"`
}

const gitTimeout = 5 * time.Second

func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	return string(out), err
}

func InGit(ctx context.Context, dir string) bool {
	out, err := git(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

func GitInit(ctx context.Context, dir string) (bool, error) {
	if InGit(ctx, dir) {
		return false, nil
	}
	if _, err := git(ctx, dir, "init", "-q", "-b", "main"); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

var aheadRe = regexp.MustCompile(`\[ahead (\d+)`)

func Status(ctx context.Context, dir string) GitState {
	st := GitState{Changes: []GitChange{}}
	if !InGit(ctx, dir) {
		return st
	}
	out, err := git(ctx, dir, "status", "--porcelain=v1", "--branch", "--untracked-files=normal", "--", ".")
	if err != nil {
		return st
	}
	st.Repo = true
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if head, ok := strings.CutPrefix(line, "## "); ok {
			branch, rest, _ := strings.Cut(head, "...")
			st.Branch = strings.TrimPrefix(branch, "No commits yet on ")
			if rest != "" {
				st.Upstream, _, _ = strings.Cut(rest, " ")
			}
			if m := aheadRe.FindStringSubmatch(head); m != nil {
				st.Ahead, _ = strconv.Atoi(m[1])
			}
			continue
		}
		if len(line) > 3 {
			st.Changes = append(st.Changes, GitChange{Status: strings.TrimSpace(line[:2]), Path: line[3:]})
		}
	}
	return st
}
