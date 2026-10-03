package main

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ilvar/strictgo/internal/capability"
)

func applyFixes(root string, diagnostics []Diagnostic) (bool, error) {
	var fixes []Fix
	for _, d := range diagnostics {
		if d.Code == "strictgo::format" {
			fixes = append(fixes, d.Fixes...)
		}
	}
	if len(fixes) == 0 && toolErrorCount(diagnostics) == 0 {
		for _, d := range diagnostics {
			if d.Code == "strictgo::no_dot_import" {
				fixes = append(fixes, d.Fixes...)
			}
		}
	}
	if len(fixes) == 0 {
		return false, nil
	}
	edits := map[string][]Edit{}
	for _, fix := range fixes {
		for _, edit := range fix.Edits {
			path := filepath.Join(root, filepath.FromSlash(edit.File))
			if !capability.IsWithin(root, path) || filepath.IsAbs(edit.File) {
				return false, fmt.Errorf("fix escapes project root: %s", edit.File)
			}
			edits[path] = append(edits[path], edit)
		}
	}
	paths := make([]string, 0, len(edits))
	for path := range edits {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	originals := map[string][]byte{}
	updated := map[string][]byte{}
	for _, path := range paths {
		real, err := capability.ResolvePath(path)
		if err != nil {
			return false, err
		}
		if real != path {
			return false, fmt.Errorf("fix refuses symlink: %s", path)
		}
		data, err := capability.ReadFile(path)
		if err != nil {
			return false, err
		}
		originals[path] = data
		list := edits[path]
		sort.Slice(list, func(i, j int) bool {
			if list[i].StartByte != list[j].StartByte {
				return list[i].StartByte > list[j].StartByte
			}
			return list[i].EndByte > list[j].EndByte
		})
		last := len(data) + 1
		for _, edit := range list {
			if edit.StartByte < 0 || edit.EndByte < edit.StartByte || edit.EndByte > len(data) || edit.EndByte > last || !utf8.Valid(data[:edit.StartByte]) || !utf8.Valid(data[:edit.EndByte]) {
				return false, fmt.Errorf("invalid or overlapping fix in %s", edit.File)
			}
			data = append(append(append([]byte{}, data[:edit.StartByte]...), []byte(edit.Replacement)...), data[edit.EndByte:]...)
			last = edit.StartByte
		}
		formatted, err := format.Source(data)
		if err != nil {
			return false, fmt.Errorf("suggested fix is not valid Go in %s: %w", path, err)
		}
		updated[path] = formatted
	}
	changed := false
	for _, path := range paths {
		if bytes.Equal(originals[path], updated[path]) {
			continue
		}
		if err := capability.WriteFile(path, updated[path]); err != nil {
			for _, prior := range paths {
				if prior == path {
					break
				}
				_ = capability.WriteFile(prior, originals[prior])
			}
			return false, err
		}
		changed = true
	}
	if !changed {
		return false, nil
	}
	beforeTools := toolErrorCount(diagnostics)
	after, err := checkOnce(root)
	if err != nil {
		rollback(paths, originals)
		return false, err
	}
	if toolErrorCount(after.Diagnostics) > beforeTools {
		rollback(paths, originals)
		return false, fmt.Errorf("suggested fix failed compiler validation and was rolled back")
	}
	return true, nil
}
func toolErrorCount(diagnostics []Diagnostic) int {
	count := 0
	for _, d := range diagnostics {
		if strings.HasPrefix(d.Code, "go::") {
			count++
		}
	}
	return count
}
func rollback(paths []string, originals map[string][]byte) {
	for _, path := range paths {
		_ = capability.WriteFile(path, originals[path])
	}
}
