// jr-readme-manifest — CLI over the manifest package. Pure computation: no
// network, no model, no LLM. Two subcommands:
//
//	manifest <dir>   print the folder's current manifest as sha256:<hex>
//	check <dir>      recompute and compare against the folder's README marker
package main

import (
	"fmt"
	"os"

	"gitlab.com/john-richter/ai/shared-tooling/go/manifest"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: jr-readme-manifest manifest <dir>")
	fmt.Fprintln(os.Stderr, "       jr-readme-manifest check <dir>")
}

func main() {
	os.Exit(run(os.Args))
}

func run(args []string) int {
	if len(args) != 3 {
		usage()
		return 2
	}
	cmd, dir := args[1], args[2]

	switch cmd {
	case "manifest":
		digest, err := manifest.Compute(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println(manifest.FormatDigest(digest))
		return 0

	case "check":
		actual, err := manifest.Compute(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		expected, err := manifest.ReadExpected(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if actual != expected {
			fmt.Fprintf(os.Stderr, "%s: manifest mismatch\n  expected: %s\n  actual:   %s\n",
				dir, manifest.FormatDigest(expected), manifest.FormatDigest(actual))
			return 1
		}
		return 0

	default:
		usage()
		return 2
	}
}
