// Command conformancereport summarises `go test -json` output of the port
// conformance suite (SRS §7.4) as pass / fail / unsupported per test (AR-012)
// and exits non-zero if anything failed.
//
//	go test -json ./internal/adapters/... | conformancereport
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
)

type event struct {
	Action  string
	Package string
	Test    string
	Output  string
}

type result struct {
	outcome string
	detail  string
}

func main() {
	results := map[string]map[string]*result{} // package -> test -> result
	output := map[string]string{}              // package/test -> accumulated output
	packageFailed := map[string]bool{}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		key := e.Package + "/" + e.Test
		switch e.Action {
		case "output":
			output[key] += e.Output
		case "pass", "fail", "skip":
			if e.Test == "" {
				if e.Action == "fail" {
					packageFailed[e.Package] = true
				}
				continue
			}
			// Only subtests of TestConformance are suite cases.
			parent, name, ok := strings.Cut(e.Test, "/")
			if !ok || parent != "TestConformance" {
				continue
			}
			r := &result{outcome: e.Action}
			if e.Action == "skip" && strings.Contains(output[key], "UNSUPPORTED:") {
				r.outcome = "unsupported"
				r.detail = strings.TrimSpace(output[key][strings.Index(output[key], "UNSUPPORTED:"):])
			}
			if results[e.Package] == nil {
				results[e.Package] = map[string]*result{}
			}
			results[e.Package][name] = r
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "read test output:", err)
		os.Exit(1)
	}

	failed := false
	pkgs := make([]string, 0, len(results))
	for p := range results {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	counts := map[string]int{}
	for _, p := range pkgs {
		fmt.Printf("%s\n", path.Base(p))
		names := make([]string, 0, len(results[p]))
		for n := range results[p] {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			r := results[p][n]
			counts[r.outcome]++
			fmt.Printf("  %-12s %s", strings.ToUpper(r.outcome), n)
			if r.detail != "" {
				fmt.Printf("  (%s)", r.detail)
			}
			fmt.Println()
			failed = failed || r.outcome == "fail"
		}
	}
	for p := range packageFailed {
		if results[p] == nil {
			fmt.Printf("%s\n  FAIL         package failed before running the suite\n", path.Base(p))
		}
		failed = true
	}
	fmt.Printf("\n%d passed, %d failed, %d unsupported, %d skipped\n", counts["pass"], counts["fail"], counts["unsupported"], counts["skip"])
	if len(results) == 0 {
		fmt.Println("no conformance results: is CCSRA_CONFORMANCE=1 set and the stack up?")
		failed = true
	}
	if failed {
		os.Exit(1)
	}
}
