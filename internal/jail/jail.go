// Package jail builds the isolated directory tree a single step item runs in.
// Nothing reaches a step except what its spec declared, which is what makes
// cadence's isolation guarantee a property of the filesystem rather than a
// convention.
package jail

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/hacKarim/cadence/internal/spec"
	"github.com/hacKarim/cadence/internal/template"
)

// Jail is one step item's private directory tree.
//
// Out sits inside Work so a step can simply write to `out/report.md` from its
// working directory. Meta sits outside it: it holds the engine's own logs and
// is never reachable from the step.
//
//	<root>/work/      the process cwd, holding staged inputs
//	<root>/work/out/  where the step writes results
//	<root>/work/tmp/  the step's own TMPDIR
//	<root>/meta/      engine-owned logs, outside the step's reach
type Jail struct {
	Root string
	Work string
	Out  string
	Tmp  string
	Meta string
}

// inputNameRe is the permitted shape of a staged input path: a relative path
// of plain segments, with no traversal.
var inputNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// envAllowlist is everything a step inherits from the parent process.
// Anything else must be declared on the step.
var envAllowlist = []string{"PATH", "HOME", "TERM", "LANG", "LC_ALL", "TZ", "SHELL", "TMPDIR"}

// agentEnvPrefixes are the variables the claude CLI needs to authenticate.
// They reach agent steps only: a shell step has no use for them, and every
// step has a network.
var agentEnvPrefixes = []string{"ANTHROPIC_", "CLAUDE_"}

// Create builds the tree for one item of one step under a run directory.
func Create(runDir, stepID string, itemIndex int) (*Jail, error) {
	root := filepath.Join(runDir, stepID, strconv.Itoa(itemIndex))
	work := filepath.Join(root, "work")
	j := &Jail{
		Root: root,
		Work: work,
		Out:  filepath.Join(work, "out"),
		Tmp:  filepath.Join(work, "tmp"),
		Meta: filepath.Join(root, "meta"),
	}
	for _, d := range []string{j.Work, j.Out, j.Tmp, j.Meta} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return nil, fmt.Errorf("create jail: %w", err)
		}
	}
	return j, nil
}

// Stage materialises the step's workdir and declared inputs inside work/.
func (j *Jail) Stage(st spec.Step, sc template.Scope) error {
	// isolate: true means the step starts from a clean working directory, so
	// a workflow-wide default workdir is not staged into it.
	if st.Workdir != "" && !st.Isolate {
		if err := copyTreeReadOnly(st.Workdir, j.Work); err != nil {
			return fmt.Errorf("stage workdir %q: %w", st.Workdir, err)
		}
	}
	for name, in := range st.Inputs {
		if !inputNameRe.MatchString(name) || strings.Contains(name, "..") {
			return fmt.Errorf("input %q: must be a relative path of plain segments inside the jail", name)
		}
		dst := filepath.Join(j.Work, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}
		switch {
		case in.Copy != "":
			src, err := template.Render(in.Copy, sc)
			if err != nil {
				return fmt.Errorf("input %q: %w", name, err)
			}
			if err := copyFile(strings.TrimSpace(src), dst, 0o600); err != nil {
				return fmt.Errorf("input %q: %w", name, err)
			}
		default:
			content, err := template.Render(in.Content, sc)
			if err != nil {
				return fmt.Errorf("input %q: %w", name, err)
			}
			if err := os.WriteFile(dst, []byte(content), 0o600); err != nil {
				return fmt.Errorf("input %q: %w", name, err)
			}
		}
	}
	return nil
}

// Env builds the child process environment. It is constructed from an
// allowlist rather than inherited, so a secret in the parent environment does
// not silently reach every step.
func (j *Jail) Env(st spec.Step, sc template.Scope, parent []string) ([]string, error) {
	keep := map[string]string{}
	for _, kv := range parent {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if allowedEnvKey(k, st.IsAgent()) {
			keep[k] = v
		}
	}
	// Declared values are templates, because the validator checks them as
	// templates and applies the needs rule to them.
	for k, v := range st.Env {
		rendered, err := template.Render(v, sc)
		if err != nil {
			return nil, fmt.Errorf("env %q: %w", k, err)
		}
		keep[k] = rendered
	}
	// The step is told where to put results, so a workflow can use an absolute
	// path as well as the relative out/ beside its working directory.
	keep["CADENCE_OUT"] = j.Out
	// Its temporary directory is its own. Handing it the host's and then
	// denying access, which is what an enforcing sandbox does, breaks mktemp,
	// git, pip and anything using the standard library's temp helpers.
	keep["TMPDIR"] = j.Tmp
	out := make([]string, 0, len(keep))
	for k, v := range keep {
		out = append(out, k+"="+v)
	}
	return out, nil
}

// RenderedEnv returns the step's declared environment after templating, for
// the cache key. A value derived from the current item has to change the key;
// a static secret must not, or rotating it would invalidate every entry.
func (j *Jail) RenderedEnv(st spec.Step, sc template.Scope) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range st.Env {
		if !strings.Contains(v, "{{") {
			continue
		}
		rendered, err := template.Render(v, sc)
		if err != nil {
			return nil, err
		}
		out[k] = rendered
	}
	return out, nil
}

func allowedEnvKey(k string, isAgent bool) bool {
	for _, a := range envAllowlist {
		if k == a {
			return true
		}
	}
	if !isAgent {
		return false
	}
	for _, p := range agentEnvPrefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

// MetaDirFor returns the engine-owned metadata directory for an item, given
// the output directory recorded in the store. The layout is defined here, so
// a reader of a finished run does not have to re-derive it.
func MetaDirFor(outDir string) string {
	if outDir == "" {
		return ""
	}
	// <root>/work/out -> <root>/meta
	return filepath.Join(filepath.Dir(filepath.Dir(outDir)), "meta")
}

// Cleanup removes the jail. The engine keeps jails of finished runs, so this
// is only used when a run is abandoned before it recorded anything.
func (j *Jail) Cleanup() error { return os.RemoveAll(j.Root) }

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// copyTreeReadOnly copies a project directory into the jail with the files
// made unwritable, so a step that was given code to run cannot quietly edit it.
func copyTreeReadOnly(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyFile(p, target, 0o444)
	})
}
