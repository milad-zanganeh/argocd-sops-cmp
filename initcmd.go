package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// chartMeta is the subset of Chart.yaml this plugin reads.
type chartMeta struct {
	Dependencies []struct {
		Repository string `yaml:"repository"`
	} `yaml:"dependencies"`
}

func readChartMeta(path string) (*chartMeta, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	var m chartMeta
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", path, err)
	}
	return &m, nil
}

func runInit() error {
	if !fileExists("Chart.yaml") {
		return nil
	}
	if dirNonEmpty("charts") {
		return nil
	}

	// Isolate Helm's repository state per invocation so concurrent renders
	// don't share repositories.yaml or the repo cache.
	cleanup, err := isolateHelmRepoState()
	if err != nil {
		return err
	}
	defer cleanup()

	meta, err := readChartMeta("Chart.yaml")
	if err != nil {
		return err
	}

	host := os.Getenv("HELM_REPO_HOST")
	user := os.Getenv("HELM_REPO_USERNAME")
	pass := os.Getenv("HELM_REPO_PASSWORD")

	for i, dep := range classicRepos(meta) {
		name := fmt.Sprintf("dep%d", i)
		args := []string{"repo", "add", "--force-update", name, dep}
		if host != "" && user != "" && strings.Contains(dep, host) {
			args = append(args, "--username", user, "--password", pass)
		}
		if out, err := runCombined("helm", args...); err != nil {
			return fmt.Errorf("helm repo add %s %s failed: %w\n%s", name, dep, err, trimmed(out))
		}
	}

	switch {
	case fileExists("Chart.lock"):
		return helmDependency("build")
	case len(meta.Dependencies) > 0:
		return helmDependency("update")
	}
	return nil
}

// isolateHelmRepoState points HELM_REPOSITORY_CONFIG/CACHE at a fresh temp dir
// so concurrent CMP invocations don't share Helm's repositories.yaml or cache.
// It returns a cleanup func that removes the temp dir (call via defer).
func isolateHelmRepoState() (func(), error) {
	tmp := os.Getenv("TMPDIR")
	if tmp == "" {
		tmp = "/tmp"
	}
	repoTmp, err := os.MkdirTemp(tmp, "helm-repo.")
	if err != nil {
		return nil, fmt.Errorf("cannot create Helm repo tmp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(repoTmp) }

	cache := filepath.Join(repoTmp, "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		cleanup()
		return nil, fmt.Errorf("cannot create Helm repo cache dir: %w", err)
	}
	os.Setenv("HELM_REPOSITORY_CONFIG", filepath.Join(repoTmp, "repositories.yaml"))
	os.Setenv("HELM_REPOSITORY_CACHE", cache)
	return cleanup, nil
}

func classicRepos(meta *chartMeta) []string {
	var out []string
	for _, dep := range meta.Dependencies {
		url := strings.TrimSpace(dep.Repository)
		if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
			out = append(out, url)
		}
	}
	return out
}

func helmDependency(op string) error {
	out, err := runCombined("helm", "dependency", op)
	if s := trimmed(out); s != "" {
		fmt.Fprintln(os.Stderr, s)
	}
	if err != nil {
		msg := fmt.Sprintf("helm dependency %s failed: %v", op, err)
		if strings.Contains(out, "no cached repository") {
			msg += "\nhint: a chart-repo index is missing (check the dependency's" +
				" repository URL, and for the private repo host that" +
				" HELM_REPO_USERNAME/HELM_REPO_PASSWORD are set)."
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}
