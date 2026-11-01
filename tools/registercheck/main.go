// Command registercheck validates coupling-register.yaml (SRS §6.1).
//
// It checks the file against the embedded JSON Schema, then checks what a
// schema cannot express: unique IDs, capability IDs prefixed by their coupling
// point, and — when -srs is given — that every referenced requirement and
// conformance test actually exists in SRS.md.
//
// Capabilities without an adapter method or conformance test are reported but
// do not fail the check: per §6.1 they are findings, not errors.
package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

//go:embed coupling-register.schema.json
var schemaJSON []byte

type register struct {
	CouplingPoints []struct {
		ID           string `yaml:"id"`
		Capabilities []struct {
			ID              string   `yaml:"id"`
			Requirement     string   `yaml:"requirement"`
			AdapterMethod   string   `yaml:"adapterMethod"`
			ConformanceTest []string `yaml:"conformanceTest"`
		} `yaml:"capabilities"`
	} `yaml:"couplingPoints"`
}

func main() {
	srsPath := flag.String("srs", "", "path to SRS.md; enables requirement and test cross-checks")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: registercheck [-srs SRS.md] coupling-register.yaml")
		os.Exit(2)
	}

	raw, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fail(err)
	}
	if err := validateSchema(raw); err != nil {
		fail(err)
	}

	var reg register
	if err := yaml.Unmarshal(raw, &reg); err != nil {
		fail(err)
	}

	var srs string
	if *srsPath != "" {
		b, err := os.ReadFile(*srsPath)
		if err != nil {
			fail(err)
		}
		srs = string(b)
	}

	var errs []string
	var noAdapter, noTest []string
	seen := map[string]bool{}
	capabilities := 0
	for _, cp := range reg.CouplingPoints {
		if seen[cp.ID] {
			errs = append(errs, "duplicate coupling point "+cp.ID)
		}
		seen[cp.ID] = true
		for _, c := range cp.Capabilities {
			capabilities++
			if seen[c.ID] {
				errs = append(errs, "duplicate capability "+c.ID)
			}
			seen[c.ID] = true
			if !strings.HasPrefix(c.ID, cp.ID+".") {
				errs = append(errs, fmt.Sprintf("capability %s is not prefixed by %s", c.ID, cp.ID))
			}
			if srs != "" {
				for req := range strings.SplitSeq(c.Requirement, ", ") {
					if !strings.Contains(srs, "| `"+req+"` |") {
						errs = append(errs, fmt.Sprintf("%s: requirement %s is not defined in the SRS", c.ID, req))
					}
				}
				for _, t := range c.ConformanceTest {
					if !strings.Contains(srs, "| `"+t+"` |") {
						errs = append(errs, fmt.Sprintf("%s: conformance test %s is not listed in SRS §7.4", c.ID, t))
					}
				}
			}
			if c.AdapterMethod == "" {
				noAdapter = append(noAdapter, c.ID)
			}
			if len(c.ConformanceTest) == 0 {
				noTest = append(noTest, c.ID)
			}
		}
	}

	fmt.Printf("%d coupling points, %d capabilities\n", len(reg.CouplingPoints), capabilities)
	report("without adapter method (no abstraction possible)", noAdapter)
	report("without conformance test (unverifiable — a finding)", noTest)
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "ERROR", e)
		}
		os.Exit(1)
	}
	fmt.Println("OK")
}

func validateSchema(raw []byte) error {
	schemaDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("coupling-register.schema.json", schemaDoc); err != nil {
		return err
	}
	schema, err := c.Compile("coupling-register.schema.json")
	if err != nil {
		return err
	}

	// Round-trip YAML through JSON so the validator sees plain JSON types.
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return err
	}
	asJSON, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(asJSON))
	if err != nil {
		return err
	}
	return schema.Validate(inst)
}

func report(label string, ids []string) {
	if len(ids) == 0 {
		return
	}
	fmt.Printf("%d %s:\n", len(ids), label)
	for _, id := range ids {
		fmt.Println("  -", id)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "ERROR", err)
	os.Exit(1)
}
