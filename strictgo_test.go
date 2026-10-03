package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func moduleFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files["go.mod"] = "module example.com/sample\n\ngo 1.23\n"
	for name, data := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
func hasCode(report Report, code string) bool {
	for _, d := range report.Diagnostics {
		if d.Code == code {
			return true
		}
	}
	return false
}
func TestTypeAwareErrorsAndBuildConstraints(t *testing.T) {
	dir := moduleFixture(t, map[string]string{"main.go": "package sample\n\nimport \"fmt\"\n\nfunc own() error { return nil }\nfunc run()       { own(); fmt.Println(\"x\") }\n", "excluded.go": "//go:build strictgo_excluded\npackage sample\nthis is invalid syntax\n"})
	report, err := check(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.ErrorCount != 2 {
		t.Fatalf("expected two ignored errors, got %+v", report)
	}
	for _, d := range report.Diagnostics {
		if d.Code != "strictgo::must_handle" {
			t.Errorf("unexpected diagnostic: %+v", d)
		}
	}
}
func TestCapabilityPackageBoundary(t *testing.T) {
	dir := moduleFixture(t, map[string]string{"main.go": "package sample\nimport \"os\"\nfunc read() { _, _ = os.ReadFile(\"x\") }\n", "capability/read.go": "package capability\nimport \"os\"\nfunc Read() { _, _ = os.ReadFile(\"x\") }\n"})
	report, err := check(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, d := range report.Diagnostics {
		if d.Code == "strictgo::capability_boundary" {
			count++
			if d.At == nil || d.At.File != "main.go" {
				t.Errorf("wrong capability diagnostic: %+v", d)
			}
		}
	}
	if count != 1 {
		t.Fatalf("want one capability diagnostic: %+v", report)
	}
}
func TestDotImportSuggestedFix(t *testing.T) {
	dir := moduleFixture(t, map[string]string{"main.go": "package sample\nimport . \"strings\"\nfunc Value() string { return ToUpper(\"x\") }\n"})
	before, err := check(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCode(before, "strictgo::no_dot_import") {
		t.Fatalf("missing dot import: %+v", before)
	}
	if len(before.Diagnostics[0].Fixes) == 0 {
		t.Fatalf("missing typed suggested fix: %+v", before)
	}
	after, err := check(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if !after.OK {
		t.Fatalf("fix failed: %+v", after)
	}
	content, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "strictgo_strings.ToUpper") {
		t.Fatalf("suggestion not applied: %s", content)
	}
}
func TestCheckAndFixFormatting(t *testing.T) {
	dir := moduleFixture(t, map[string]string{"main.go": "package sample\nfunc Value() int {return 1}\n"})
	before, err := check(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if before.OK || !hasCode(before, "strictgo::format") {
		t.Fatalf("unexpected report: %+v", before)
	}
	after, err := check(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if !after.OK {
		t.Fatalf("fix failed: %+v", after)
	}
}
func TestNewProject(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()
	if err := createProject("sample"); err != nil {
		t.Fatal(err)
	}
	report, err := check(filepath.Join(dir, "sample"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("new project fails check: %+v", report)
	}
	if err := createProject("sample"); err == nil {
		t.Fatal("overwrote existing project")
	}
}

func TestFuzzTargetsRespectBuildConstraints(t *testing.T) {
	dir := moduleFixture(t, map[string]string{"main.go": "package sample\nfunc Value(v string) string { return v }\n", "main_test.go": "package sample\nimport \"testing\"\nfunc FuzzValue(f *testing.F) { f.Add(\"x\"); f.Fuzz(func(t *testing.T,v string) { if Value(v)!=v { t.Fatal(\"changed\") } }) }\n", "excluded_test.go": "//go:build strictgo_excluded\npackage sample\nimport \"testing\"\nfunc FuzzExcluded(f *testing.F) {}\n"})
	targets, err := fuzzTargets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].name != "FuzzValue" || targets[0].pattern != "." {
		t.Fatalf("unexpected fuzz targets: %+v", targets)
	}
}

func TestInvalidFixLeavesFileUnchanged(t *testing.T) {
	dir := moduleFixture(t, map[string]string{"main.go": "package sample\nfunc Value() int { return 1 }\n"})
	file := filepath.Join(dir, "main.go")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := []Diagnostic{{Code: "strictgo::format", Fixes: []Fix{{Message: "bad edit", Edits: []Edit{{File: "main.go", StartByte: 0, EndByte: len(before), Replacement: "invalid Go"}}}}}}
	if changed, err := applyFixes(dir, diagnostics); err == nil || changed {
		t.Fatalf("invalid fix accepted: changed=%t err=%v", changed, err)
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("invalid fix changed source")
	}
}

func TestTestGateReportsFailureDetail(t *testing.T) {
	dir := moduleFixture(t, map[string]string{"main.go": "package sample\n", "main_test.go": "package sample\nimport \"testing\"\nfunc TestFailure(t *testing.T) { t.Fatal(\"deliberate failure\") }\n"})
	diagnostics := runTestGate(dir, "test", []string{"test", "-json", "./..."})
	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0].Message, "deliberate failure") {
		t.Fatalf("missing failure detail: %+v", diagnostics)
	}
}
