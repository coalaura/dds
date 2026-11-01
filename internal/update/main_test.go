package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatchAnchors(t *testing.T) {
	for name, changes := range patches {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "native", "DirectXTex", "DirectXTex", filepath.Base(name))

			bundled, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			// Recover the pinned input from the bundled patched source, offline.
			text := strings.ReplaceAll(string(bundled), "\r\n", "\n")
			original := text

			for _, change := range changes {
				original = strings.ReplaceAll(original, change.text, change.anchor)
			}

			if strings.HasSuffix(name, "DirectXTexP.h") {
				original, _, _ = strings.Cut(original, "\n// MinGW's assignment-based macro")
			}

			patched, err := patch(name, []byte(original))
			if err != nil || !bytes.Equal(patched, []byte(text)) {
				t.Fatalf("pinned patch differs: %v", err)
			}

			for _, change := range changes {
				mutations := []string{strings.ReplaceAll(original, change.anchor, ""), original + change.anchor}

				for _, mutation := range mutations {
					patched, err = patch(name, []byte(mutation))
					if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "expected") || patched != nil {
						t.Fatalf("missing/ambiguous anchor %q returned data or unactionable error: %v", change.anchor, err)
					}
				}
			}
		})
	}

	t.Run("SAL", func(t *testing.T) {
		name := "DirectX-Headers/basetsd.h"
		text := "prefix\n// SAL annotations\n#define old_sal\n// Calling conventions\nsuffix\n"

		patched, err := patch(name, []byte(text))
		if err != nil {
			t.Fatal(err)
		}

		result := string(patched)
		if !strings.HasPrefix(result, "prefix\n") || !strings.HasSuffix(result, "// Calling conventions\nsuffix\n") || !strings.Contains(result, "#include <sal.h>") || !strings.Contains(result, "#undef __valid") || strings.Contains(result, "old_sal") {
			t.Fatalf("incorrect SAL adaptation: %s", result)
		}

		anchors := []string{"// SAL annotations", "// Calling conventions"}

		for _, anchor := range anchors {
			mutations := []string{strings.ReplaceAll(text, anchor, ""), text + anchor}

			for _, mutation := range mutations {
				patched, err = patch(name, []byte(mutation))
				if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), anchor) || patched != nil {
					t.Fatalf("SAL anchor %q returned data or unactionable error: %v", anchor, err)
				}
			}
		}

		patched, err = patch(name, []byte("// Calling conventions\n// SAL annotations"))
		if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "must precede") || patched != nil {
			t.Fatalf("reversed SAL anchors: %v", err)
		}
	})
}
