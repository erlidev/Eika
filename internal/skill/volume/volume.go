// Package volume is the fixed set of operations on the skills volume, run in
// the private installer computer through an executor rooted at the volume.
// Core never writes the volume itself, and nothing here takes a path, a
// command or arguments from an agent: callers pass a skill name and the files
// of a validated archive, and both are checked again here.
package volume

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/skill"
)

// Root is where the skills volume is mounted in the installer computer.
const Root = "/var/lib/eika/skills"

const (
	stageDir = ".stage"
	oldDir   = ".old"
)

// namePattern is the skill directory name rule of the specification. A name
// never starts with a dot, so it cannot collide with the working directories.
var namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Volume provides the fixed operations through an executor.
type Volume struct {
	executor executor.Executor
}

// New uses an executor rooted at the skills volume.
func New(ex executor.Executor) *Volume { return &Volume{executor: ex} }

func checkName(name string) error {
	if len(name) > 64 || !namePattern.MatchString(name) {
		return fmt.Errorf("invalid skill name")
	}
	return nil
}

// checkPath accepts only a clean relative path of plain names.
func checkPath(p string) error {
	if p == "" || p != path.Clean(p) || path.IsAbs(p) || strings.ContainsAny(p, "\x00\\") {
		return fmt.Errorf("invalid file path %q", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid file path %q", p)
		}
	}
	return nil
}

// Stage writes the files of a skill into its staging directory, replacing any
// earlier staging of the same name, and leaves the installed skill alone.
func (v *Volume) Stage(ctx context.Context, name string, files []skill.File) error {
	if err := checkName(name); err != nil {
		return err
	}
	for _, f := range files {
		if err := checkPath(f.Path); err != nil {
			return err
		}
	}
	dir := stageDir + "/" + name
	if err := v.sh(ctx, `rm -rf -- "$1" && mkdir -p -- "$1"`, nil, dir); err != nil {
		return fmt.Errorf("clear staging: %w", err)
	}
	for _, f := range files {
		p := dir + "/" + f.Path
		if err := v.executor.WriteFile(ctx, p, f.Data); err != nil {
			return fmt.Errorf("stage %s: %w", f.Path, err)
		}
		mode := "644"
		if f.Mode&0o111 != 0 {
			mode = "755"
		}
		if err := v.sh(ctx, `chmod "$2" -- "$1"`, nil, p, mode); err != nil {
			return fmt.Errorf("stage %s: %w", f.Path, err)
		}
	}
	return nil
}

// Replace makes the staged tree of name the installed skill, moving any
// installed one aside first so a failure never leaves a half-written
// directory under the skill's name.
func (v *Volume) Replace(ctx context.Context, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	return v.sh(ctx, `set -e
test -d "$1/$2"
mkdir -p -- "$3"
rm -rf -- "$3/$2"
if [ -e "$2" ]; then mv -- "$2" "$3/$2"; fi
mv -- "$1/$2" "$2"
rm -rf -- "$3/$2"`, nil, stageDir, name, oldDir)
}

// Remove deletes an installed skill and anything staged for it. A skill that
// is not there is not an error.
func (v *Volume) Remove(ctx context.Context, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	return v.sh(ctx, `rm -rf -- "$1" "$2/$1" "$3/$1"`, nil, name, stageDir, oldDir)
}

// List names the top-level entries of the volume that are not the working
// directories, sorted. Names are returned as found, valid or not, so an
// unexpected entry can be reported.
func (v *Volume) List(ctx context.Context) ([]string, error) {
	var out bytes.Buffer
	if err := v.sh(ctx, `find . -mindepth 1 -maxdepth 1 ! -name '.*' -printf '%f\n' | LC_ALL=C sort`, &out); err != nil {
		return nil, err
	}
	var names []string
	for _, n := range strings.Split(out.String(), "\n") {
		if n != "" {
			names = append(names, n)
		}
	}
	return names, nil
}

// Hash returns a hex SHA-256 over the paths, modes and contents of the
// installed skill's files, or fs.ErrNotExist if it is not installed.
func (v *Volume) Hash(ctx context.Context, name string) (string, error) {
	return v.hash(ctx, name, name)
}

// HashStaged is Hash for a staged tree, so an install can compare what was
// written with what it meant to write before swapping it in.
func (v *Volume) HashStaged(ctx context.Context, name string) (string, error) {
	return v.hash(ctx, name, stageDir+"/"+name)
}

func (v *Volume) hash(ctx context.Context, name, dir string) (string, error) {
	if err := checkName(name); err != nil {
		return "", err
	}
	var out bytes.Buffer
	err := v.sh(ctx, `set -e
test -d "$1" || exit 3
cd "$1"
find . -type f | LC_ALL=C sort | while IFS= read -r f; do
  printf '%s %s %s\n' "$(stat -c %a -- "$f")" "$(sha256sum < "$f" | cut -d' ' -f1)" "$f"
done | sha256sum | cut -d' ' -f1`, &out, dir)
	if err != nil {
		if strings.Contains(err.Error(), "exit 3") {
			return "", fs.ErrNotExist
		}
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// sh runs a fixed script with the given positional arguments. Nothing from a
// caller is part of the script text.
func (v *Volume) sh(ctx context.Context, script string, stdout *bytes.Buffer, args ...string) error {
	var stderr bytes.Buffer
	spec := executor.ExecSpec{
		Command: "sh", Args: append([]string{"-c", script, "sh"}, args...),
		Timeout: 60 * time.Second, Stderr: &stderr,
	}
	if stdout != nil {
		spec.Stdout = stdout
	}
	res, err := v.executor.Exec(ctx, spec)
	if err != nil {
		return fmt.Errorf("run skills operation: %w", err)
	}
	if res.ExitCode != 0 || res.TimedOut {
		return fmt.Errorf("run skills operation: exit %d: %s", res.ExitCode, strings.TrimSpace(stderr.String()))
	}
	return nil
}
