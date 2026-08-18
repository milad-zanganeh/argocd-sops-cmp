package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func TestClassicReposFiltersAndKeepsOrder(t *testing.T) {
	meta := &chartMeta{}
	meta.Dependencies = []struct {
		Repository string `yaml:"repository"`
	}{
		{Repository: "https://example.com/charts"},
		{Repository: "oci://registry.example.com/charts"},
		{Repository: "file://../../charts/local"},
		{Repository: "  http://plain.example.com/charts  "}, // trimmed
		{Repository: ""},
	}

	got := classicRepos(meta)
	want := []string{"https://example.com/charts", "http://plain.example.com/charts"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBuildHelmArgsValid(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "values.yaml"), "a: 1\n")
	if err := os.MkdirAll(filepath.Join(dir, "env"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "env", "prod.yaml"), "b: 2\n")
	chdir(t, dir)

	t.Setenv("ARGOCD_ENV_HELM_VALUE_FILES", "values.yaml, env/prod.yaml")
	t.Setenv("ARGOCD_ENV_HELM_PARAMETERS", "image.tag=1")

	args, err := buildHelmArgs()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--values") || !strings.HasSuffix(args[1], "/values.yaml") {
		t.Fatalf("expected first values file resolved, got %v", args)
	}
	if !strings.HasSuffix(args[3], "/env/prod.yaml") {
		t.Fatalf("expected second values file resolved, got %v", args)
	}
	if args[len(args)-2] != "--set" || args[len(args)-1] != "image.tag=1" {
		t.Fatalf("expected --set image.tag=1, got %v", args)
	}
}

func TestBuildHelmArgsRejects(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "ok.yaml"), "a: 1\n")
	outside := filepath.Join(dir, "..", "outside.yaml")
	mustWrite(t, outside, "x: 1\n")
	chdir(t, dir)

	cases := map[string]string{
		"absolute":  "/etc/passwd",
		"missing":   "nope.yaml",
		"traversal": "../outside.yaml",
	}
	for name, val := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("ARGOCD_ENV_HELM_VALUE_FILES", val)
			if _, err := buildHelmArgs(); err == nil {
				t.Fatalf("expected error for %s (%q), got nil", name, val)
			}
		})
	}
}

func TestSopsFilesOrder(t *testing.T) {
	dir := t.TempDir()
	sec := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(sec, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"b.yaml", "a.yaml", "z.yml"} {
		mustWrite(t, filepath.Join(sec, f), "k: v\n")
	}
	chdir(t, dir)

	got := sopsFiles("secrets")
	want := []string{
		filepath.Join("secrets", "a.yaml"),
		filepath.Join("secrets", "b.yaml"),
		filepath.Join("secrets", "z.yml"), // .yml group comes after .yaml
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
