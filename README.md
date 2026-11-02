# dds

Go DDS decoding, encoding and complete-texture processing, backed by the bundled CPU implementation of Microsoft's DirectXTex. Importing `github.com/coalaura/dds` registers DDS with `image.Decode` and `image.DecodeConfig`.

## Install

```sh
go get github.com/coalaura/dds
```

Requires Go 1.27.3 or later and **CGO with a working C/C++17 toolchain** and its normal platform headers/libraries. GCC/MinGW, Clang and Zig-based toolchains are suitable; macOS needs its platform SDK. The vendored codec compiles during ordinary `go build`. No codec installation, CMake, pkg-config, generation step, shader compiler, external converter or runtime download is needed. The codec is linked into the binary; Linux/Windows request static C++ runtime linkage, while Darwin uses the system libc++.

## Images

```go
decoded, err := dds.Decode(reader)
if err != nil {
	return err
}
// decoded is an image.Image with its own pixels.
_ = decoded
```

```go
err := dds.Encode(writer, sourceImage, &dds.Options{
	Format: dds.FormatBC7_UNORM,
})
```

Nil encoding options select lossless uncompressed RGBA8/RGBA16 storage, preserving source precision and straight/premultiplied alpha. BC encoding is lossy and has fixed block sizes. Decoding selects base mip, first array element/cube face and first depth slice. `DecodeWithOptions` selects another surface or explicitly converts signed/HDR data for display; the default refuses conversions that would silently lose its range.

## Complete textures

```go
texture, err := dds.Load(reader, nil)
if err != nil {
	return err
}
metadata := texture.Metadata()
face, err := texture.Surface(dds.CubeSelection(0, dds.FacePositiveX, 0))
if err != nil {
	return err
}
_ = metadata
_ = face
```

`Texture.Save` preserves native pixels without recompression. `Convert`, `Compress`, `Decompress`, `GenerateMipmaps`, `Interpret` and `WithAlphaMode` return immutable textures. All storage is Go-owned; there is no `Close` requirement. `NewTexture` copies caller buffers, and surface/image access returns independent data. See the [Go documentation](https://pkg.go.dev/github.com/coalaura/dds) for format, pitch, alpha and option semantics.

`Inspect`/`InspectAt` read only the header, without pixel decompression. `LoadAt` accepts `io.ReaderAt` plus a size; loading reads the complete texture into bounded storage. Headers may be normalized when saving. When default loading changes a legacy pixel layout, `OriginalDDS` retains the source file and default saving preserves it; `HeaderDX10` explicitly writes the normalized representation.

## Coverage and limits

- Legacy/DX10 DDS; BC1-BC7, BC4/5 SNORM, BC6H unsigned/signed float, applicable sRGB variants; upstream-supported uncompressed, packed, integer and floating-point layouts.
- Mip chains, arrays, cube arrays and volume slices are retained. Loading/saving support is broader than image conversion or filtering, particularly for planar, video and depth layouts. `Format.Info` describes storage, not processing support; unsupported operations return errors. Typeless processing requires an explicit compatible interpretation.
- DXT1/3/5 and ATI1/ATI2 map to their BC equivalents; DXT2/4 retain premultiplied alpha. BC1 supports one-bit alpha; BC6H has no alpha. Decompress BC input before conversion or mip generation.
- CPU-only, single-threaded operations; independent operations may run concurrently. No OpenMP requirement, WIC, GPU compression, automatic tone mapping, normal-map reconstruction or game-specific swizzles. Unknown FourCCs and RXGB are rejected.

The ordinary image API preserves UNORM precision with 8-bit or 16-bit Go images. `DisplayStrict` rejects signed, integer and float input; `DisplayClamp` explicitly converts to UNORM, clamping floats to [0,1], mapping SNORM from [-1,1] to [0,1] and applying upstream integer normalization. Use native surfaces for HDR/data textures. Color conversion follows explicit flags and sRGB format identity; Go images preserve encoded sRGB samples by default but carry no color-space tag.

The default input/payload budget is 512 MiB, with dimensions limited to 32768 and subresources to 65536. `LoadOptions.MaxBytes` can raise the loading budget to 2 GiB minus one byte; processing destinations remain capped at 512 MiB. These limits do not cap total process memory: processing also needs native output and scratch storage. Loading validates source pitches and payload size before native pixel allocation.

## Platforms

Linux, Windows and macOS on amd64 and arm64, with portable CPU baselines.

## Development

<details>
<summary>Bundled revisions, updates and fixtures</summary>

Packaging follows coalaura/webp's committed source-wrapper approach. Only the DirectXTex CPU backend is compiled; Direct3D declarations are included where required, but resource creation, GPU codecs, tools/viewers and WIC implementations are excluded.

| Upstream | Exact revision | Bundled subset |
| --- | --- | --- |
| microsoft/DirectXTex | `ec66b78b37389e8373a8e14ccc3cd29be94146b3` | CPU DDS, BC codecs, conversion, storage, mipmaps, alpha and filtering utilities |
| microsoft/DirectXMath | `f5d82574b03417d905fdd2f85bfa9bf7a2f1deac` | Math and packed-vector headers/inlines |
| microsoft/DirectX-Headers | `faf928dc6feb8b86e9e3d6a39fc5171de7d8ac36` | DXGI, required D3D declarations and WSL compatibility stubs |
| dotnet/runtime | `80aa709f5d919c6814726788dc6dabe23e79e672` (v9.0.2) | `src/coreclr/pal/inc/rt/sal.h` |

From the repository root, update with Go, Git and network access, then run the checks:

```sh
go run ./internal/update
builder test go --cgo --compat --no-gen ./...
vet --cgo ./...
```

Pins live in `internal/update/main.go`. The command copies selected sources/licenses, applies portability changes and generates `z_*.cpp` wrappers and DXGI constants. Existing checkouts can be supplied with `-source <directory>` containing `dds-DirectXTex`, `dds-DirectXMath` and `dds-DirectX-Headers`. Commit the generated outputs; consumers never run this command or `go generate`.

When updating, review the isolated adaptations: lowercase `windows.h`, `DDS_NO_WIC` guards that retain platform allocators, portable `UNREFERENCED_PARAMETER`, shared SAL definitions with the unused `__valid` macro removed to avoid a libstdc++ collision, and `internal/native/dds_preflight.inc` for source-layout validation before allocation. Recheck patch anchors and upstream pitch/expansion rules, then run the native/cross-target workflow.

Regenerate fixtures with:

```sh
builder run go --cgo --compat --no-gen --pkg ./internal/fixtures
```

FFmpeg is needed only to regenerate independent PNG references; ordinary tests need no external tools. The `legacy-*` DDS fixtures are hand-built from DDS and BC block layouts independently of DirectXTex. DXT1/3/5 and RGB24 PNG references were decoded with FFmpeg 8.0.1 (libavcodec 62.11.100). DXT2/4 exercise premultiplied alpha, ATI1/2 cover BC4/5 mappings, and the 1x1 BC1 fixture uses the transparent selector.

Other fixtures use the pinned DirectXTex CPU backend: constant 5x3 samples cover every BC family, signed/HDR variants and packed/high-precision storage; coordinate-coded samples cover arrays, cubes, mips, 1D and volume textures. These are integration checks rather than independent codec evidence. Source patterns and generation logic are in `internal/fixtures`; all fixture patterns are original to this repository and distributed under its license.

</details>

## License

[MPL 2.0 licensed](LICENSE), copyright (c) 2026 coalaura. DirectXTex, DirectXMath, DirectX-Headers and compatibility headers retain their upstream licenses/notices under `internal/native`.
