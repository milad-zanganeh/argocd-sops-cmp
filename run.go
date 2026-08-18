package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func run(stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = stdin
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func runCombined(name string, args ...string) (string, error) {
	var buf bytes.Buffer
	err := run(nil, &buf, &buf, name, args...)
	return buf.String(), err
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func dirNonEmpty(p string) bool {
	entries, err := os.ReadDir(p)
	return err == nil && len(entries) > 0
}

func sopsFiles(dir string) []string {
	var out []string
	for _, pat := range []string{"*.yaml", "*.yml"} {
		m, _ := filepath.Glob(filepath.Join(dir, pat))
		sort.Strings(m)
		out = append(out, m...)
	}
	return out
}

func trimmed(s string) string { return strings.TrimSpace(s) }
