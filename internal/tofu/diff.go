package tofu

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
	"sigs.k8s.io/yaml"
)

const (
	markSensitive = "(sensitive)"
	markChanged   = "(sensitive, changed)"
	markUnknown   = "(known after apply)"
	maxDiffLines  = 400
)

var hiddenFields = []string{"id", "uid", "status", "manifest", "metadata", "timeouts"}

type change struct {
	Actions         []string `json:"actions"`
	Before          any      `json:"before"`
	After           any      `json:"after"`
	AfterUnknown    any      `json:"after_unknown"`
	BeforeSensitive any      `json:"before_sensitive"`
	AfterSensitive  any      `json:"after_sensitive"`
}

func resourceDiff(address, kind, action string, c change) []string {
	before, after := redact(c.Before, c.After, c.BeforeSensitive, c.AfterSensitive, c.AfterUnknown)
	head := fmt.Sprintf("# %s (%s)", address, action)
	b, a := project(kind, before), project(kind, after)
	if slices.Equal(b, a) {
		return append([]string{head}, changedNames(before, after)...)
	}
	text, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{A: terminated(b), B: terminated(a), Context: 3})
	return append([]string{head}, strings.Split(strings.TrimRight(text, "\n"), "\n")...)
}

func redact(b, a, bMask, aMask, unknown any) (any, any) {
	if isTrue(unknown) {
		return redactSide(b, bMask, aMask), markUnknown
	}
	if isTrue(bMask) || isTrue(aMask) {
		var rb, ra any
		if b != nil {
			rb = markSensitive
		}
		if a != nil {
			ra = markSensitive
			if b != nil && !reflect.DeepEqual(a, b) {
				ra = markChanged
			}
		}
		return rb, ra
	}
	bm, bIsMap := b.(map[string]any)
	am, aIsMap := a.(map[string]any)
	um, _ := unknown.(map[string]any)
	if bIsMap || aIsMap {
		keys := map[string]bool{}
		for _, m := range []map[string]any{bm, am, um} {
			for k := range m {
				keys[k] = true
			}
		}
		var ob, oa map[string]any
		if bIsMap {
			ob = map[string]any{}
		}
		if aIsMap || len(um) > 0 {
			oa = map[string]any{}
		}
		for k := range keys {
			rb, ra := redact(bm[k], am[k], at(bMask, k), at(aMask, k), um[k])
			if ob != nil && rb != nil {
				ob[k] = rb
			}
			if oa != nil && ra != nil {
				oa[k] = ra
			}
		}
		return ob, oa
	}
	bl, bIsList := b.([]any)
	al, aIsList := a.([]any)
	ul, _ := unknown.([]any)
	if names, ok := namedElements(bl, al); ok {
		bi, ai := indexByName(bl), indexByName(al)
		var ob, oa []any
		for _, n := range names {
			var b, a, bm, am, u any
			if i, ok := bi[n]; ok {
				b, bm = bl[i], index(listOf(bMask), i)
			}
			if i, ok := ai[n]; ok {
				a, am, u = al[i], index(listOf(aMask), i), index(ul, i)
			}
			rb, ra := redact(b, a, bm, am, u)
			if rb != nil {
				ob = append(ob, rb)
			}
			if ra != nil {
				oa = append(oa, ra)
			}
		}
		return ob, oa
	}
	if bIsList || aIsList {
		var ob, oa []any
		for i := range max(len(bl), len(al)) {
			rb, ra := redact(index(bl, i), index(al, i), index(listOf(bMask), i), index(listOf(aMask), i), index(ul, i))
			if i < len(bl) {
				ob = append(ob, rb)
			}
			if i < len(al) {
				oa = append(oa, ra)
			}
		}
		return ob, oa
	}
	return b, a
}

func namedElements(lists ...[]any) ([]string, bool) {
	seen := map[string]bool{}
	for _, l := range lists {
		inList := map[string]bool{}
		for _, e := range l {
			m, ok := e.(map[string]any)
			name, named := m["name"].(string)
			if !ok || !named || inList[name] {
				return nil, false
			}
			inList[name], seen[name] = true, true
		}
	}
	if len(seen) == 0 {
		return nil, false
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, true
}

func indexByName(l []any) map[string]int {
	out := map[string]int{}
	for i, e := range l {
		out[e.(map[string]any)["name"].(string)] = i
	}
	return out
}

func redactSide(v, bMask, aMask any) any {
	r, _ := redact(v, nil, bMask, aMask, nil)
	return r
}

func isTrue(v any) bool {
	t, _ := v.(bool)
	return t
}

func at(mask any, key string) any {
	m, _ := mask.(map[string]any)
	return m[key]
}

func listOf(mask any) []any {
	l, _ := mask.([]any)
	return l
}

func index(l []any, i int) any {
	if i < len(l) {
		return l[i]
	}
	return nil
}

func project(kind string, v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		if v == nil {
			return nil
		}
		return []string{fmt.Sprint(v)}
	}
	switch kind {
	case "helm_release":
		return helmLines(m)
	case "kubectl_manifest":
		if m["kind"] == "Secret" {
			return []string{markSensitive}
		}
		return textLines(m["yaml_body_parsed"])
	}
	var out []string
	if md := metadataOf(m); md != nil {
		out = append(out, field("metadata", md)...)
	}
	for _, k := range sortedKeys(m) {
		if slices.Contains(hiddenFields, k) || strings.HasSuffix(k, "_incluster") || strings.HasPrefix(k, "live_") || m[k] == nil {
			continue
		}
		out = append(out, field(k, m[k])...)
	}
	return out
}

func helmLines(m map[string]any) []string {
	var out []string
	for _, k := range []string{"repository", "chart", "version", "namespace"} {
		if m[k] != nil {
			out = append(out, fmt.Sprintf("%s: %v", k, m[k]))
		}
	}
	if s, ok := m["values"].(string); ok {
		out = append(out, "values: "+s)
	}
	values, _ := m["values"].([]any)
	for _, v := range values {
		out = append(out, "values:")
		for _, l := range textLines(v) {
			out = append(out, "  "+l)
		}
	}
	for _, key := range []string{"set", "set_list", "set_sensitive"} {
		if s, ok := m[key].(string); ok {
			out = append(out, key+": "+s)
			continue
		}
		entries, _ := m[key].([]any)
		var lines []string
		for _, e := range entries {
			s, ok := e.(map[string]any)
			if !ok {
				lines = append(lines, fmt.Sprintf("%s %v", key, e))
				continue
			}
			lines = append(lines, fmt.Sprintf("%s %v = %v", key, s["name"], s["value"]))
		}
		sort.Strings(lines)
		out = append(out, lines...)
	}
	return out
}

func metadataOf(m map[string]any) map[string]any {
	list, _ := m["metadata"].([]any)
	if len(list) == 0 {
		return nil
	}
	md, ok := list[0].(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for _, k := range []string{"name", "namespace", "labels", "annotations"} {
		if md[k] != nil {
			out[k] = md[k]
		}
	}
	return out
}

func field(key string, v any) []string {
	switch v.(type) {
	case map[string]any, []any:
		b, err := yaml.Marshal(v)
		if err != nil {
			return []string{fmt.Sprintf("%s: %v", key, v)}
		}
		out := []string{key + ":"}
		for _, l := range textLines(string(b)) {
			out = append(out, "  "+l)
		}
		return out
	}
	return []string{fmt.Sprintf("%s: %v", key, v)}
}

func textLines(v any) []string {
	s, ok := v.(string)
	if !ok {
		if v == nil {
			return nil
		}
		return []string{fmt.Sprint(v)}
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func changedNames(before, after any) []string {
	bm, _ := before.(map[string]any)
	am, _ := after.(map[string]any)
	var names, later []string
	for _, k := range sortedKeys(am) {
		switch {
		case am[k] == markUnknown:
			later = append(later, k+" "+markUnknown)
		case !reflect.DeepEqual(bm[k], am[k]):
			names = append(names, k)
		}
	}
	if len(names) > 0 && !slices.ContainsFunc(names, func(k string) bool { return !strings.HasSuffix(k, "_incluster") && !strings.HasPrefix(k, "live_") }) {
		return []string{"live object differs from the declared manifest"}
	}
	if names = append(names, later...); len(names) == 0 {
		return nil
	}
	return []string{"changed: " + strings.Join(names, ", ")}
}

func terminated(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l + "\n"
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func capLines(lines []string) string {
	if len(lines) > maxDiffLines {
		more := len(lines) - maxDiffLines
		lines = append(lines[:maxDiffLines:maxDiffLines], fmt.Sprintf("# %d more lines", more))
	}
	return strings.Join(lines, "\n")
}
