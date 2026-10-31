# Capabilities

`Format` constants retain exact DXGI identities: typeless, UNORM, SNORM, UINT, SINT, float, and sRGB are distinct. `Format.Info` describes storage properties; it does not certify every processing operation. Operations return an error when DirectXTex does not implement the requested combination.

| Representation | Native load/save | Ordinary Go image | CPU processing |
| --- | --- | --- | --- |
| BC1/2/3 and BC7, linear/sRGB | Yes | RGBA8, alpha convention retained | Compression/decompression; decompress before filtering |
| BC4/5 UNORM/SNORM | Yes | UNORM directly; SNORM requires explicit display conversion | R/RG UNORM or SNORM decompression, compression |
| BC6H UF16/SF16 | Yes | Explicit display conversion only | Float/HDR compression and RGBA16 float decompression |
| Common RGB/RGBA/BGRA, grayscale/alpha, 565/5551/4444, 10-bit packed | Yes | Upstream conversion to RGBA8/16 | Conversion/filtering where upstream supports it |
| Higher-bit-depth UNORM, integer, float | Yes | UNORM uses 16-bit Go images; integer/float require explicit display conversion | Native signed/HDR precision retained when destination permits |
| Typeless | Yes | Explicit compatible `Interpretation` required | `Interpret` before processing |
| Other upstream DDS layouts, including planar/video/depth layouts | Where upstream accepts them | Not universally available | Not universally available; no WIC fallback |

The generated format enum includes values that DirectXTex may not accept. It is a precise vocabulary, not a claim that every enum value is a codec. Compression only accepts typed BC destinations; decompress BC sources before conversion or mip generation. BC1 supports opaque and one-bit alpha through its threshold. BC6H has no alpha channel. BC4/5 channels are data channels: no missing normal component is reconstructed. Color-space conversions follow explicit flags and sRGB format identity, never the compression family alone.

Legacy DXT1/3/5 and ATI1/ATI2 map to their BC equivalents. DXT2/4 retain premultiplied alpha. Legacy channel masks may require upstream normalization to a DXGI layout. For default loads whose legacy payload changes during normalization, `OriginalDDS` retains the source file and default saving writes it unchanged; `HeaderDX10` explicitly writes the normalized representation. Other native load/save paths preserve pixel payload and supported semantic metadata, but may normalize headers. Explicit load-conversion flags opt out of original-file retention. Unknown FourCCs and RXGB game swizzles are rejected.

## Ownership and resource limits

Textures are immutable and concurrent independent operations are safe. `NewTexture` copies caller buffers; `Surface`, `OriginalDDS` and `Image` return independent data. Native allocations are released before returning. A loaded texture retains all mip/array/face/depth surfaces, with row/slice pitches; it is not an animation.

Header inspection reads 128 or 148 bytes and validates the declared layout. `Load`/`LoadAt` read the file once into a bounded buffer, then copy the native result once into Go-owned storage. Loading is not lazy or zero-copy. Legacy preservation retains that input buffer only when needed. Processing can temporarily hold source, native destination and Go destination plus upstream scratch storage; the byte budget limits input/payload sizes, not total process memory. Processing destinations are capped at 512 MiB; `LoadOptions.MaxBytes` can adjust the loading budget up to 2 GiB minus one byte. All inputs enforce positive dimensions, valid mip counts, bounded subresource counts and checked pitch/allocation arithmetic. Native source-layout preflight rejects truncated files before pixel allocation.

`DisplayStrict` refuses signed, integer and float data in the ordinary image API. `DisplayClamp` explicitly requests upstream conversion to UNORM: floats clamp to [0,1], SNORM maps [-1,1] to [0,1], and integer conversion follows DirectXTex's integer normalization. There is no exposure control or tone mapper. Use native surfaces for HDR/data textures. Go images have no color-space tag; image conversion preserves encoded sRGB samples unless conversion flags explicitly request otherwise.

## Platform validation

| Target | Final executable link on development host | Tests actually executed |
| --- | --- | --- |
| Windows amd64 | Yes, Zig, baseline CPU | Fixture suite |
| Windows arm64 | Yes, Zig | Not locally executed |
| Linux amd64 | Yes, Zig/musl, static | Not locally executed |
| Linux arm64 | Yes, Zig/musl, static | Not locally executed |
| Darwin amd64 | Not verified | Not locally executed |
| Darwin arm64 | Not verified | Not locally executed |

Darwin C++ sources compile. The Windows-host Go linker fails replacing its open Mach-O output during UUID rewriting (`rename a.out~ a.out: Access is denied`). Disabling the Go build ID bypasses that step but exposes the missing macOS `resolv` system library in this cross-toolchain; disabling the UUID with `-B none` instead hits Zig's unsupported `-no_uuid` argument. A native macOS SDK/toolchain or a correctly provisioned Linux-host cross-toolchain is required to complete verification. Darwin is a CI target, not an advertised verified platform yet.

CI runs fixture tests on native Linux amd64/arm64, Windows amd64 and macOS amd64/arm64 runners, and links the six-target matrix with coalaura/build. The workflow has been added but has not been executed as part of local delivery. No build uses `-march=native` or requires OpenMP.
