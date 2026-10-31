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

`Texture.Save` preserves native pixels without recompression. `Convert`, `Compress`, `Decompress`, `GenerateMipmaps`, `Interpret` and `WithAlphaMode` return immutable textures. All storage is Go-owned; there is no `Close` requirement. `Inspect`/`InspectAt` read only the header, without pixel decompression. `LoadAt` accepts `io.ReaderAt` plus a size. See [capabilities](CAPABILITIES.md) and the Go documentation for format, pitch, alpha and option semantics.

## Coverage and limits

- Legacy/DX10 DDS; BC1-BC7, BC4/5 SNORM, BC6H unsigned/signed float, applicable sRGB variants; upstream-supported uncompressed, packed, integer and floating-point layouts.
- Mip chains, arrays, cube arrays and volume slices are retained. Loading/saving support is broader than image conversion or filtering. Typeless processing requires an explicit compatible interpretation.
- Linux amd64/arm64 and Windows amd64/arm64 have linked successfully; fixture tests have run on Windows amd64. Darwin amd64/arm64 native sources compile, but final cross-linking on the development Windows host is blocked; Darwin is not yet verified. CI includes native macOS tests. See [platform details](CAPABILITIES.md#platform-validation).
- CPU-only, single-threaded operations; independent operations may run concurrently. No WIC, GPU compression, automatic tone mapping, normal-map reconstruction or game-specific swizzles. Default input/payload budget: 512 MiB; dimensions: 32768; subresources: 65536.

MIT licensed. DirectXTex, DirectXMath, DirectX-Headers and compatibility headers retain their upstream licenses/notices under `internal/native`. Exact pins and reproducible update instructions are in [MAINTAINING.md](MAINTAINING.md). Packaging follows coalaura/webp's committed source-wrapper approach.
