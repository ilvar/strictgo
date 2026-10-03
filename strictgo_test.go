package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanFilePolicyLocations(t *testing.T) {
	source := []byte("package p\nimport \"os\"\nvar count int\nfunc f() { panic(\"bad\"); _ = os.ReadFile(\"a\") }\n")
	got := scanFile("main.go", source)
	codes := map[string]int{}
	for _, d := range got {
		codes[d.Code]++
		if d.At == nil || d.At.File != "main.go" || d.At.Start.Line < 1 || d.At.Snippet == "" {
			t.Fatalf("bad location: %+v", d)
		}
	}
	for _, code := range []string{"strictgo::no_mutable_global", "strictgo::no_panic", "strictgo::capability_boundary"} {
		if codes[code] != 1 {
			t.Errorf("%s count = %d", code, codes[code])
		}
	}
}
func TestCheckAndFixFormatting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/sample\n\ngo 1.23\n"), 0644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte("package sample\nfunc Value() int {return 1}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := check(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if before.OK || before.ErrorCount != 1 || before.Diagnostics[0].Code != "strictgo::format" {
		t.Fatalf("unexpected report: %+v", before)
	}
	after, err := check(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if !after.OK {
		t.Fatalf("fix failed: %+v", after)
	}
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "func Value() int { return 1 }") {
		t.Fatalf("file not formatted: %s", content)
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
	if _, err := os.Stat(filepath.Join(dir, "sample", "go.mod")); err != nil {
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
