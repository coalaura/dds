# Fixtures

Regenerate from the repository root with `builder run go --cgo --compat --no-gen --pkg ./internal/fixtures`. Requires FFmpeg only for generating independent PNG references; ordinary tests need no external tools.

`legacy-*` DDS files are hand-built from the DDS and BC block layouts by `internal/fixtures`, independently of DirectXTex. DXT1/3/5 and RGB24 PNG references were decoded with FFmpeg 8.0.1 (libavcodec 62.11.100). DXT2/4 encode premultiplied samples; ATI1/2 exercise legacy BC4/5 mappings. The 1x1 BC1 fixture uses the transparent selector.

Other fixtures are generated through the pinned DirectXTex CPU backend: constant 5x3 samples exercise every BC family, signed/HDR variants, packed/16-bit/float/integer storage; coordinate-coded fixtures exercise arrays, cube arrays, mip chains, 1D and volume textures. These integration fixtures are not independent codec evidence. Their source values and generation logic are in the small generator.

All fixture patterns are original to this repository and distributed under its license.
