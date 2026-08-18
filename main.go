// Command argocd-sops-cmp is an Argo CD Config Management Plugin (CMP) that renders a
// Helm chart and appends the SOPS-decrypted Secret manifests found in the app's
// optional secrets/ directory.
//
// It exposes three subcommands, wired 1:1 to the CMP phases in plugin.yaml:
//
//	discover  print "match" if this dir is a Helm chart or a secrets-only app
//	init      resolve Helm chart dependencies (add repos, dependency build/update)
//	generate  render the chart + emit decrypted secrets to stdout
//
// Only rendered manifests go to stdout; every diagnostic goes to stderr.
package main

import (
	"fmt"
	"os"
)

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "argocd-sops-cmp: "+format+"\n", a...)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		fail("usage: argocd-sops-cmp <discover|init|generate>")
	}

	var err error
	switch cmd := os.Args[1]; cmd {
	case "discover":
		err = runDiscover()
	case "init":
		err = runInit()
	case "generate":
		err = runGenerate()
	default:
		fail("unknown subcommand %q (want discover|init|generate)", cmd)
	}

	if err != nil {
		fail("%s: %v", os.Args[1], err)
	}
}
