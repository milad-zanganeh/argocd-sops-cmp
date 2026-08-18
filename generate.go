package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func runGenerate() error {
	gpg, err := newGPG()
	if err != nil {
		return err
	}
	defer gpg.cleanup()
	if err := gpg.prepare(); err != nil {
		return err
	}

	helmArgs, err := buildHelmArgs()
	if err != nil {
		return err
	}

	// Render the chart, unless this is a chart-less (secrets-only) app.
	if fileExists("Chart.yaml") {
		if err := helmTemplate(helmArgs); err != nil {
			return err
		}
	}

	return gpg.decryptSecrets()
}

func helmTemplate(extra []string) error {
	release := os.Getenv("ARGOCD_ENV_HELM_RELEASE_NAME")
	if release == "" {
		app := os.Getenv("ARGOCD_APP_NAME")
		release = app[strings.LastIndex(app, "/")+1:]
	}
	if release == "" {
		release = "release"
	}
	ns := os.Getenv("ARGOCD_APP_NAMESPACE")
	if ns == "" {
		ns = "default"
	}

	args := append([]string{"template", release, ".",
		"--namespace", ns, "--include-crds"}, extra...)

	var errb strings.Builder
	err := run(nil, os.Stdout, &errb, "helm", args...)
	if s := trimmed(errb.String()); s != "" {
		fmt.Fprintln(os.Stderr, s)
	}
	if err != nil {
		msg := fmt.Sprintf("helm template failed: %v", err)
		if strings.Contains(errb.String(), "nil pointer evaluating") {
			msg += "\nhint: for an umbrella chart, overrides in values.yaml must be" +
				" nested under the dependency-name (or alias) key, or they never" +
				" reach the subchart."
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func buildHelmArgs() ([]string, error) {
	var args []string

	if raw := os.Getenv("ARGOCD_ENV_HELM_VALUE_FILES"); raw != "" {
		root, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("cannot determine app source dir: %w", err)
		}
		if canonical, err := filepath.EvalSymlinks(root); err == nil {
			root = canonical // fully resolved, so the prefix check is symlink-safe
		}

		for _, entry := range strings.Split(raw, ",") {
			vf := strings.TrimSpace(entry)
			if vf == "" {
				continue
			}
			if filepath.IsAbs(vf) {
				return nil, fmt.Errorf("values file must be a repo-relative path: %s", vf)
			}
			if !fileExists(vf) {
				return nil, fmt.Errorf("values file not found: %s", vf)
			}

			abs, err := filepath.Abs(vf)
			if err != nil {
				return nil, fmt.Errorf("values file not found: %s", vf)
			}
			resolved, err := filepath.EvalSymlinks(abs)
			if err != nil {
				return nil, fmt.Errorf("values file not found: %s", vf)
			}
			if resolved != root && !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
				return nil, fmt.Errorf("values file escapes the repository: %s", vf)
			}
			args = append(args, "--values", resolved)
		}
	}

	if params := os.Getenv("ARGOCD_ENV_HELM_PARAMETERS"); params != "" {
		args = append(args, "--set", params)
	}

	return args, nil
}
