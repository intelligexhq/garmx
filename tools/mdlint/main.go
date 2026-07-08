// Command mdlint checks or fixes Markdown formatting across the repository.
//
// With no flags it reports every Markdown file whose content is not in
// canonical form and exits non-zero (used by `make lint-md`). With -fix it
// rewrites those files in place (used by `make fmt-md`). Paths may be given as
// arguments; with none it walks the current directory. Build/VCS directories
// are skipped.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/intelligexhq/garmx/internal/mdlint"
)

// warn writes a diagnostic to stderr; write errors on diagnostics are ignored
// intentionally.
func warn(args ...any) { _, _ = fmt.Fprintln(os.Stderr, args...) }

// info writes an informational line to stdout; write errors are ignored.
func info(args ...any) { _, _ = fmt.Fprintln(os.Stdout, args...) }

func main() {
	fix := flag.Bool("fix", false, "rewrite non-canonical files in place")
	flag.Parse()

	roots := flag.Args()
	if len(roots) == 0 {
		roots = []string{"."}
	}

	files, err := collect(roots)
	if err != nil {
		warn("mdlint:", err)
		os.Exit(2)
	}

	var bad []string
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			warn("mdlint:", err)
			os.Exit(2)
		}
		got := mdlint.Normalize(src)
		if bytes.Equal(got, src) {
			continue
		}
		if *fix {
			if err := os.WriteFile(f, got, 0o644); err != nil {
				warn("mdlint:", err)
				os.Exit(2)
			}
			info("fixed", f)
			continue
		}
		bad = append(bad, f)
	}

	if len(bad) > 0 {
		warn("not canonical Markdown (run 'make fmt-md'):")
		for _, f := range bad {
			warn("  " + f)
		}
		os.Exit(1)
	}
}

// collect gathers *.md files under the given roots, skipping build and VCS
// directories and de-duplicating overlapping roots.
func collect(roots []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", "bin", "node_modules":
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".md") || seen[path] {
				return nil
			}
			seen[path] = true
			out = append(out, path)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
