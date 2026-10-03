package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ilvar/strictgo/internal/capability"
	"golang.org/x/tools/go/packages"
)

func thorough(path string) (Report, error) {
	root, err := moduleRoot(path)
	if err != nil {
		return Report{}, err
	}
	report, err := checkOnce(root)
	if err != nil || !report.OK {
		return report, err
	}
	report.Diagnostics = append(report.Diagnostics, runTestGate(root, "test", []string{"test", "-json", "./..."})...)
	report.Diagnostics = append(report.Diagnostics, runTestGate(root, "race", []string{"test", "-race", "-json", "./..."})...)
	targets, err := fuzzTargets(root)
	if err != nil {
		return Report{}, err
	}
	for _, target := range targets {
		args := []string{"test", "-json", "-run", "^$", "-fuzz", "^" + target.name + "$", "-fuzztime=2s", "-parallel=1", target.pattern}
		report.Diagnostics = append(report.Diagnostics, runTestGate(root, "fuzz", args)...)
	}
	finishReport(&report)
	return report, nil
}

type fuzzTarget struct{ name, pattern string }

func fuzzTargets(root string) ([]fuzzTarget, error) {
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedCompiledGoFiles | packages.NeedSyntax, Dir: root, Tests: true}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var result []fuzzTarget
	for _, pkg := range pkgs {
		for i, file := range pkg.Syntax {
			if i >= len(pkg.CompiledGoFiles) {
				continue
			}
			path := pkg.CompiledGoFiles[i]
			if !capability.IsWithin(root, path) || !strings.HasSuffix(path, "_test.go") {
				continue
			}
			rel, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return nil, err
			}
			pattern := "."
			if rel != "." {
				pattern = "./" + filepath.ToSlash(rel)
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || !strings.HasPrefix(fn.Name.Name, "Fuzz") || len(fn.Name.Name) == 4 {
					continue
				}
				key := pattern + "/" + fn.Name.Name
				if seen[key] {
					continue
				}
				seen[key] = true
				result = append(result, fuzzTarget{fn.Name.Name, pattern})
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].pattern != result[j].pattern {
			return result[i].pattern < result[j].pattern
		}
		return result[i].name < result[j].name
	})
	return result, nil
}
func runTestGate(root, kind string, args []string) []Diagnostic {
	output, err := capability.RunGo(root, args...)
	if err == nil {
		return nil
	}
	type event struct{ Action, Package, Test, Output, OutputType string }
	failedTests := map[string]map[string]bool{}
	failedPackages := map[string]bool{}
	races := map[string]bool{}
	errorLines := map[string][]string{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	for scanner.Scan() {
		var e event
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		if e.Test != "" && (e.OutputType == "error" || strings.Contains(e.Output, ".go:")) {
			line := strings.TrimSpace(e.Output)
			key := e.Package + "/" + e.Test
			if line != "" && len(errorLines[key]) < 3 {
				errorLines[key] = append(errorLines[key], line)
			}
		}
		if strings.Contains(e.Output, "WARNING: DATA RACE") {
			races[e.Package] = true
		}
		if e.Action != "fail" {
			continue
		}
		if e.Test != "" {
			if failedTests[e.Package] == nil {
				failedTests[e.Package] = map[string]bool{}
			}
			failedTests[e.Package][e.Test] = true
		} else if e.Package != "" {
			failedPackages[e.Package] = true
		}
	}
	var out []Diagnostic
	for pkg, tests := range failedTests {
		for test := range tests {
			out = append(out, Diagnostic{Level: "error", Source: "go " + kind, Code: "go::" + kind, Message: testFailureMessage(kind, pkg, test, errorLines[pkg+"/"+test])})
		}
	}
	for pkg := range failedPackages {
		if len(failedTests[pkg]) == 0 {
			out = append(out, Diagnostic{Level: "error", Source: "go " + kind, Code: "go::" + kind, Message: fmt.Sprintf("%s failed: %s", kind, pkg)})
		}
	}
	for pkg := range races {
		out = append(out, Diagnostic{Level: "error", Source: "go race", Code: "go::race", Message: "data race detected in " + pkg})
	}
	if len(out) == 0 {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		if len(message) > 1200 {
			message = message[:1200]
		}
		out = append(out, Diagnostic{Level: "error", Source: "go " + kind, Code: "go::" + kind, Message: message})
	}
	return out
}

func testFailureMessage(kind, pkg, test string, details []string) string {
	message := fmt.Sprintf("%s failed: %s/%s", kind, pkg, test)
	if len(details) > 0 {
		message += ": " + strings.Join(details, "; ")
	}
	return message
}
