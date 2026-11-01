// Command update vendors the pinned CPU backend and regenerates CGO wrappers.
package main

import (
	"flag"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type dependency struct {
	name       string
	repository string
	revision   string
	directory  string
}

type replacement struct {
	anchor string
	text   string
	count  int
}

var patches = map[string][]replacement{
	"DirectXTex/DirectXTexConvert.cpp": {{"#ifndef _WIN32", "#if !defined(_WIN32) || defined(DDS_NO_WIC)", 2}},
	"DirectXTex/DirectXTexMipmaps.cpp": {
		{"#ifdef _WIN32", "#if defined(_WIN32) && !defined(DDS_NO_WIC)", 5},
		// Alias both row pointers when a box-filtered mip becomes one pixel high.
		{
			anchor: "            if (height <= 1)\n            {\n                urow1 = urow0;\n            }",
			text:   "            if (height <= 1)\n            {\n                urow1 = urow0;\n                urow3 = urow2;\n            }",
			count:  1,
		},
	},
	"DirectXTex/DirectXTexResize.cpp": {{"#ifdef _WIN32", "#if defined(_WIN32) && !defined(DDS_NO_WIC)", 6}},
	"DirectXTex/DirectXTexUtil.cpp": {
		{"#ifdef _WIN32", "#if defined(_WIN32) && !defined(DDS_NO_WIC)", 2},
		{"#else // !WIN32", "#elif !defined(_WIN32)", 1},
	},
	"DirectXTex/DirectXTexP.h": {{"<Windows.h>", "<windows.h>", 1}},
	"DirectXTex/DirectXTexDDS.cpp": {{
		anchor: "    size_t remaining = size - offset;\n    if (remaining == 0)\n        return E_FAIL;\n",
		text:   "    size_t remaining = size - offset;\n    if (remaining == 0)\n        return E_FAIL;\n\n#include \"../../dds_preflight.inc\"\n",
		count:  1,
	}},
}

var dependencies = []dependency{
	{"DirectXTex", "microsoft/DirectXTex", "ec66b78b37389e8373a8e14ccc3cd29be94146b3", "DirectXTex"},
	{"DirectXMath", "microsoft/DirectXMath", "f5d82574b03417d905fdd2f85bfa9bf7a2f1deac", "Inc"},
	{"DirectX-Headers", "microsoft/DirectX-Headers", "faf928dc6feb8b86e9e3d6a39fc5171de7d8ac36", "include"},
}

var sources = []string{"BC", "BC4BC5", "BC6HBC7", "DirectXTexCompress", "DirectXTexConvert", "DirectXTexDDS", "DirectXTexImage", "DirectXTexMipmaps", "DirectXTexMisc", "DirectXTexPMAlpha", "DirectXTexResize", "DirectXTexUtil"}

func main() {
	cache := flag.String("source", "", "directory containing dds-DirectXTex, dds-DirectXMath and dds-DirectX-Headers checkouts")
	flag.Parse()

	err := update(*cache)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func update(cache string) error {
	if cache == "" {
		var err error

		cache, err = os.MkdirTemp("", "dds-update-")
		if err != nil {
			return err
		}

		defer os.RemoveAll(cache)
	}

	for _, dependency := range dependencies {
		checkout := filepath.Join(cache, "dds-"+dependency.name)

		_, err := os.Stat(checkout)
		if os.IsNotExist(err) {
			command := exec.Command("git", "clone", "https://github.com/"+dependency.repository+".git", checkout)
			command.Stdout = os.Stdout
			command.Stderr = os.Stderr

			err = command.Run()
			if err != nil {
				return err
			}
		}

		command := exec.Command("git", "-C", checkout, "checkout", "--detach", dependency.revision)

		err = command.Run()
		if err != nil {
			return err
		}

		destination := filepath.Join("internal", "native", dependency.name)

		err = os.RemoveAll(destination)
		if err != nil {
			return err
		}

		err = filepath.WalkDir(checkout, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}

			relative, relErr := filepath.Rel(checkout, path)
			if relErr != nil {
				return relErr
			}

			if entry.IsDir() {
				if relative != "." && !strings.HasPrefix(relative, dependency.directory) {
					return filepath.SkipDir
				}

				return nil
			}

			base := filepath.Base(path)
			license := strings.Contains(strings.ToLower(base), "license") || strings.Contains(strings.ToLower(base), "notice")
			extension := filepath.Ext(path)
			wanted := extension == ".h" || extension == ".inl"

			if dependency.name == "DirectX-Headers" && !strings.Contains(relative, "wsl") {
				wanted = base == "d3d12.h" || base == "d3d12sdklayers.h" || base == "d3dcommon.h" || base == "dxgicommon.h" || base == "dxgiformat.h"
			}

			if dependency.name == "DirectXMath" {
				wanted = wanted && (strings.HasPrefix(base, "DirectXMath") || strings.HasPrefix(base, "DirectXPackedVector"))
			}

			if dependency.name == "DirectXTex" {
				wanted = false

				for _, source := range sources {
					wanted = wanted || base == source+".cpp"
				}

				wanted = wanted || base == "DirectXTex.h" || base == "DirectXTex.inl" || base == "DirectXTexP.h" || base == "BC.h" || base == "DDS.h" || base == "filters.h" || base == "scoped.h"
			}

			if !wanted && !license {
				return nil
			}

			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}

			data, patchErr := patch(dependency.name+"/"+base, data)
			if patchErr != nil {
				return patchErr
			}

			return write(filepath.Join(destination, relative), data)
		})

		if err != nil {
			return err
		}
	}

	for _, name := range sources {
		text := "// Code generated by go run ./internal/update; DO NOT EDIT.\n#include \"internal/native/DirectXTex/DirectXTex/" + name + ".cpp\"\n"

		err := write("z_"+name+".cpp", []byte(text))
		if err != nil {
			return err
		}
	}

	salFiles := []string{"src/coreclr/pal/inc/rt/sal.h", "LICENSE.TXT", "THIRD-PARTY-NOTICES.TXT"}

	for _, name := range salFiles {
		response, err := http.Get("https://raw.githubusercontent.com/dotnet/runtime/80aa709f5d919c6814726788dc6dabe23e79e672/" + name)
		if err != nil {
			return err
		}

		data, err := io.ReadAll(response.Body)
		response.Body.Close()

		if err != nil {
			return err
		}

		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("download %s: %s", name, response.Status)
		}

		err = write(filepath.Join("internal/native/sal", filepath.Base(name)), data)
		if err != nil {
			return err
		}
	}

	data, err := os.ReadFile("internal/native/DirectX-Headers/include/directx/dxgiformat.h")
	if err != nil {
		return err
	}

	expression := regexp.MustCompile(`DXGI_FORMAT_([A-Z0-9_]+)\s*=\s*(0[xX][0-9a-fA-F]+|[0-9]+)`)

	var output strings.Builder

	output.WriteString("// Code generated by go run ./internal/update; DO NOT EDIT.\npackage dds\n\nconst (\n")

	for _, match := range expression.FindAllSubmatch(data, -1) {
		fmt.Fprintf(&output, "Format%s Format = %s\n", match[1], match[2])
	}

	output.WriteString(")\n\n// Format is an exact DXGI_FORMAT value. Payloads use little-endian DXGI layouts.\ntype Format uint32\n")

	formatted, err := format.Source([]byte(output.String()))
	if err != nil {
		return err
	}

	return write("format_generated.go", formatted)
}

func patch(name string, data []byte) ([]byte, error) {
	if !strings.HasPrefix(name, "DirectXTex/") && name != "DirectX-Headers/basetsd.h" {
		return data, nil
	}

	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	// Only these source files' WIN32 guards control WIC code. Keep platform
	// allocation and file I/O guards in the other files intact.
	for _, change := range patches[name] {
		err := checkAnchor(name, text, change.anchor, change.count)
		if err != nil {
			return nil, err
		}

		text = strings.ReplaceAll(text, change.anchor, change.text)
	}

	if name == "DirectXTex/DirectXTexP.h" {
		text += "\n// MinGW's assignment-based macro rejects const references.\n#undef UNREFERENCED_PARAMETER\n#define UNREFERENCED_PARAMETER(value) (void)(value)\n"
	}

	if name == "DirectX-Headers/basetsd.h" {
		annotations := "// Use the complete bundled SAL definitions shared with DirectXMath.\n#include <sal.h>\n\n"
		annotations += "// The unused SAL1 macro collides with libstdc++'s integer parser.\n#undef __valid\n\n"

		var err error

		text, err = replaceSection(name, text, "// SAL annotations", "// Calling conventions", annotations)
		if err != nil {
			return nil, err
		}
	}

	return []byte(text), nil
}

func checkAnchor(name, text, anchor string, expected int) error {
	count := strings.Count(text, anchor)
	if count != expected {
		return fmt.Errorf("patch %s: anchor %q: expected %d matches, got %d; check the pinned upstream source and patch", name, anchor, expected, count)
	}

	return nil
}

func replaceSection(name, text, start, end, replacement string) (string, error) {
	err := checkAnchor(name, text, start, 1)
	if err != nil {
		return "", err
	}

	err = checkAnchor(name, text, end, 1)
	if err != nil {
		return "", err
	}

	first := strings.Index(text, start)
	last := strings.Index(text, end)

	if last <= first {
		return "", fmt.Errorf("patch %s: anchor %q must precede %q; check the pinned upstream source and patch", name, start, end)
	}

	return text[:first] + replacement + text[last:], nil
}

func write(path string, data []byte) error {
	err := os.MkdirAll(filepath.Dir(path), 0755)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
