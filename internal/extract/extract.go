// Package extract lifts structured values out of what a step produced. It is
// the only place that interprets a step's raw bytes, so every extraction rule
// in the spec language is testable in one spot.
package extract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/hacKarim/cadence/internal/spec"
)

// Sources is everything one finished step item can be read from.
type Sources struct {
	Stdout string
	Result string
	Dir    string // the item's out/ directory
}

// Primary returns an agent step's primary result: the whole stdout when no
// extract block is set, otherwise the text the block selects.
func Primary(stdout string, e *spec.Extract) (string, error) {
	if e == nil {
		return stdout, nil
	}
	switch {
	case e.After != "":
		lines := strings.Split(stdout, "\n")
		for i, l := range lines {
			if strings.Contains(l, e.After) {
				return strings.Join(lines[i+1:], "\n"), nil
			}
		}
		return "", fmt.Errorf("marker %q not found in output", e.After)
	case e.Regex != "":
		return firstGroup(e.Regex, stdout)
	}
	return stdout, nil
}

func firstGroup(pattern, s string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", err
	}
	m := re.FindStringSubmatch(s)
	if m == nil {
		return "", fmt.Errorf("pattern %q did not match the output", pattern)
	}
	if len(m) > 1 {
		return m[1], nil
	}
	return m[0], nil
}

// Output resolves one declared output against a finished step item.
func Output(o spec.OutputSpec, src Sources) (any, error) {
	raw, err := rawFor(o, src)
	if err != nil {
		return nil, err
	}
	if o.Regex != "" {
		if raw, err = firstGroup(o.Regex, raw); err != nil {
			return nil, err
		}
	}
	switch o.As {
	case spec.ParseLines:
		return strings.Split(strings.TrimRight(raw, "\n"), "\n"), nil
	case spec.ParseJSON:
		var v any
		if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &v); err != nil {
			return nil, fmt.Errorf("expected JSON, got %.80q: %w", raw, err)
		}
		if o.Select == "" {
			return v, nil
		}
		return selectPath(v, o.Select)
	default:
		return raw, nil
	}
}

func rawFor(o spec.OutputSpec, src Sources) (string, error) {
	switch o.From {
	case spec.OutputFromStdout:
		return src.Stdout, nil
	case spec.OutputFromResult:
		return src.Result, nil
	case spec.OutputFromFile:
		p, err := safeJoin(src.Dir, o.Path)
		if err != nil {
			return "", err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("output file %q: %w", o.Path, err)
		}
		return string(b), nil
	}
	return "", fmt.Errorf("unknown output source %q", o.From)
}

// safeJoin refuses any path that would leave the step's own directory. A step
// reads its own output, never a neighbour's.
func safeJoin(dir, rel string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("no step directory to read %q from", rel)
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("output path %q must be relative to the step directory", rel)
	}
	joined := filepath.Join(dir, rel)
	cleanDir := filepath.Clean(dir) + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(joined)+string(filepath.Separator), cleanDir) {
		return "", fmt.Errorf("output path %q escapes the step directory", rel)
	}
	// The engine reads this file itself, outside the sandbox, so a symlink
	// the step dropped must not be followed out of the directory.
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	realPath, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", fmt.Errorf("output file %q: %w", rel, err)
	}
	if !strings.HasPrefix(realPath+string(filepath.Separator), filepath.Clean(realDir)+string(filepath.Separator)) {
		return "", fmt.Errorf("output path %q resolves outside the step directory", rel)
	}
	return joined, nil
}

var segmentRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*)((?:\[\d+\])*)$`)

// selectPath resolves the deliberately small subset of JSONPath the spec
// documents: $.a.b and $.a[0].b. Anything else is rejected rather than
// silently returning nothing.
func selectPath(v any, path string) (any, error) {
	if !strings.HasPrefix(path, "$.") {
		return nil, fmt.Errorf("select %q must start with $.", path)
	}
	cur := v
	for _, seg := range strings.Split(strings.TrimPrefix(path, "$."), ".") {
		m := segmentRe.FindStringSubmatch(seg)
		if m == nil {
			return nil, fmt.Errorf("select %q: segment %q is not supported (use $.a.b or $.a[0].b)", path, seg)
		}
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("select %q: %q is not inside an object", path, m[1])
		}
		next, ok := obj[m[1]]
		if !ok {
			return nil, fmt.Errorf("select %q: key %q is not present", path, m[1])
		}
		cur = next
		for _, idx := range regexp.MustCompile(`\[(\d+)\]`).FindAllStringSubmatch(m[2], -1) {
			arr, ok := cur.([]any)
			if !ok {
				return nil, fmt.Errorf("select %q: %q is not an array", path, m[1])
			}
			i, _ := strconv.Atoi(idx[1])
			if i < 0 || i >= len(arr) {
				return nil, fmt.Errorf("select %q: index %d is out of range (len %d)", path, i, len(arr))
			}
			cur = arr[i]
		}
	}
	return cur, nil
}
