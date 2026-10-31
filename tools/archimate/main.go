// Command archimate turns docs/architecture/model.yaml into an ArchiMate 3.1
// exchange file that Archi, Sparx EA and other modelling tools can import.
//
//	go run ./tools/archimate        (or: make archimate)
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	in := flag.String("model", "docs/architecture/model.yaml", "the model to read")
	out := flag.String("out", "docs/architecture/model.archimate.xml", "the exchange file to write")
	flag.Parse()
	m, err := load(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if errs := m.validate(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "%s: %v\n", *in, e)
		}
		os.Exit(1)
	}
	if err := os.WriteFile(*out, render(m), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
