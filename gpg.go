package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// gpgEnv holds the ephemeral GnuPG state used to SOPS-decrypt secrets. The
// passphrase-protected private key is imported into a throwaway GNUPGHOME and
// its passphrase preset into gpg-agent so `sops --decrypt` runs unattended.
type gpgEnv struct {
	home       string
	keyFile    string
	keygrip    string
	passphrase string
}

func newGPG() (*gpgEnv, error) {
	keygrip := os.Getenv("SOPS_GPG_KEYGRIP")
	if keygrip == "" {
		return nil, fmt.Errorf("SOPS_GPG_KEYGRIP is required")
	}
	passphrase := os.Getenv("SOPS_GPG_PASSPHRASE")
	if passphrase == "" {
		return nil, fmt.Errorf("SOPS_GPG_PASSPHRASE is required")
	}
	keyFile := os.Getenv("SOPS_GPG_KEY_FILE")
	if keyFile == "" {
		keyFile = "/argocd-sops-cmp/keys/private-key.asc"
	}
	if !fileExists(keyFile) {
		return nil, fmt.Errorf("GPG private key not found at %s", keyFile)
	}

	tmp := os.Getenv("TMPDIR")
	if tmp == "" {
		tmp = "/tmp"
	}
	home, err := os.MkdirTemp(tmp, "gnupghome.")
	if err != nil {
		return nil, fmt.Errorf("cannot create GNUPGHOME: %w", err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		_ = os.RemoveAll(home)
		return nil, fmt.Errorf("cannot secure GNUPGHOME: %w", err)
	}
	os.Setenv("GNUPGHOME", home)

	return &gpgEnv{home: home, keyFile: keyFile, keygrip: keygrip, passphrase: passphrase}, nil
}

func (g *gpgEnv) cleanup() {
	_ = run(nil, nil, nil, "gpgconf", "--kill", "gpg-agent")
	_ = os.RemoveAll(g.home)
}

func (g *gpgEnv) prepare() error {
	// gpg-agent must accept preset passphrases (the key is passphrase-protected).
	conf := filepath.Join(g.home, "gpg-agent.conf")
	if err := os.WriteFile(conf, []byte("allow-preset-passphrase\n"), 0o600); err != nil {
		return fmt.Errorf("cannot write gpg-agent.conf: %w", err)
	}

	var errb strings.Builder
	if err := run(nil, nil, &errb, "gpgconf", "--launch", "gpg-agent"); err != nil {
		return fmt.Errorf("cannot launch gpg-agent: %w\n%s", err, trimmed(errb.String()))
	}

	errb.Reset()
	if err := run(nil, nil, &errb, "gpg", "--batch", "--quiet", "--no-tty", "--import", g.keyFile); err != nil {
		return fmt.Errorf("cannot import GPG private key: %w\n%s", err, trimmed(errb.String()))
	}

	// The passphrase is passed on stdin (never argv) and all output discarded.
	if err := run(strings.NewReader(g.passphrase), nil, nil,
		"/usr/lib/gnupg/gpg-preset-passphrase", "--preset", g.keygrip); err != nil {
		return fmt.Errorf("cannot preset GPG passphrase "+
			"(check SOPS_GPG_KEYGRIP matches the key, and the passphrase): %w", err)
	}
	return nil
}

func (g *gpgEnv) decryptSecrets() error {
	if !dirExists("secrets") {
		return nil
	}
	for _, f := range sopsFiles("secrets") {
		fmt.Fprint(os.Stdout, "\n---\n")
		var errb strings.Builder
		if err := run(nil, os.Stdout, &errb, "sops", "--decrypt", f); err != nil {
			return fmt.Errorf("sops --decrypt %s failed: %w\n%s", f, err, trimmed(errb.String()))
		}
	}
	return nil
}
