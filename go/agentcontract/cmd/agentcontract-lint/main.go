// Command agentcontract-lint is the CI front-end for package agentcontract: it discovers every
// agent-brief roster under one or more given roots, lints each against the discriminator-matrix
// and instruction-property rules, and prints a deterministic report.
//
// Exit codes: 0 clean (zero findings, including a tree with no rosters at all); 1 one or more
// findings; 2 usage or discovery error (a brief failed to parse, a root does not exist).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/johnrichter/claude-shared-tooling/go/agentcontract"
)

func main() {
	fs := flag.NewFlagSet("agentcontract-lint", flag.ExitOnError)
	var schemaRoots stringSlice
	fs.Var(&schemaRoots, "schema-root", "additional root an output_schema path may resolve against, beyond each brief's own directory (repeatable)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	roots := fs.Args()
	if len(roots) == 0 {
		roots = []string{"."}
	}

	opts := agentcontract.Options{SchemaRoots: []string(schemaRoots)}

	overallPass := true
	for _, root := range roots {
		report, err := agentcontract.Lint(root, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agentcontract-lint: %v\n", err)
			os.Exit(2)
		}
		if err := report.Render(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "agentcontract-lint: writing report: %v\n", err)
			os.Exit(2)
		}
		if !report.Pass() {
			overallPass = false
		}
	}

	if !overallPass {
		os.Exit(1)
	}
}

// stringSlice implements flag.Value to collect repeated -schema-root flags.
type stringSlice []string

func (s *stringSlice) String() string {
	return fmt.Sprint([]string(*s))
}

func (s *stringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}
