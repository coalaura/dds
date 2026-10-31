# Native backend

| Upstream | Exact revision | Bundled subset |
| --- | --- | --- |
| microsoft/DirectXTex | `ec66b78b37389e8373a8e14ccc3cd29be94146b3` | CPU DDS, BC codecs, conversion, image storage, mipmaps, alpha, filtering utilities |
| microsoft/DirectXMath | `f5d82574b03417d905fdd2f85bfa9bf7a2f1deac` | Math and packed-vector headers/inlines |
| microsoft/DirectX-Headers | `faf928dc6feb8b86e9e3d6a39fc5171de7d8ac36` | DXGI, required D3D declaration headers and WSL compatibility stubs |
| dotnet/runtime | `80aa709f5d919c6814726788dc6dabe23e79e672` (v9.0.2) | `src/coreclr/pal/inc/rt/sal.h` |

Licenses and notices are copied alongside each subset. DirectXTex's declaration headers mention Direct3D, but resource creation, GPU codecs, tools/viewers and WIC implementations are not compiled. `bridge.h` is the only C interface; no native type escapes into the public API.

## Update

From the repository root, with Go, Git and network access:

```sh
go run ./internal/update
```

Pins live in `internal/update/main.go`; change them deliberately before updating. The command copies selected sources/licenses, applies isolated portability changes, generates the root `z_*.cpp` translation-unit wrappers and exact DXGI format constants. For existing checkouts use `-source <directory>` containing `dds-DirectXTex`, `dds-DirectXMath` and `dds-DirectX-Headers`. Only this maintainer command downloads anything. Commit its outputs; consumers never run it or `go generate`.

Review the diff against upstream, especially changed patch anchors and newly required headers. The small adaptations are: lowercase `windows.h` for case-sensitive toolchains; `DDS_NO_WIC` guards around WIC-only branches while retaining native platform allocators; a portable `UNREFERENCED_PARAMETER`; using one complete SAL header instead of conflicting stub macros; and `internal/native/dds_preflight.inc` to check on-disk payload sizes before `ScratchImage` allocation. The preflight mirrors upstream source-pitch expansion rules, including malformed-tail and permissive-cube options; recheck these when updating DDS loading.

Run `builder test go --cgo --compat --no-gen ./...` and `vet --cgo ./...`, then the native/cross-target workflow. A local link probe is `builder build go linux --arch arm64 --cgo --compat --no-gen --pkg ./internal/probe --out .build/probe-linux-arm64` (create `.build` first). Fixture regeneration and independent FFmpeg references are described in [testdata/README.md](testdata/README.md). FFmpeg is a maintainer reference tool only, never a package dependency.
