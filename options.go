package dds

import (
	"fmt"
	"math"
)

const (
	// LoadLegacyDWORD reads legacy rows padded to DWORD boundaries.
	LoadLegacyDWORD LoadFlags = 0x1
	// LoadNoLegacyExpansion rejects layouts that need expansion to a DXGI format.
	LoadNoLegacyExpansion LoadFlags = 0x2
	// LoadNoR10Fixup disables the historical R10G10B10A2 channel-order correction.
	LoadNoR10Fixup LoadFlags = 0x4
	// LoadForceRGB converts legacy BGR layouts to RGB layouts while loading.
	LoadForceRGB LoadFlags = 0x8
	// LoadExpand16Bit expands legacy 565, 5551 and 4444 storage to RGBA8.
	LoadExpand16Bit LoadFlags = 0x10
	// LoadExpandLuminance replicates legacy luminance into RGB channels.
	LoadExpandLuminance LoadFlags = 0x20
	// LoadBadDXTTails accepts old incorrectly sized block-compressed mip tails.
	LoadBadDXTTails LoadFlags = 0x40
	// LoadPermissive enables upstream repairs of selected malformed header fields.
	// Inspection cannot perform repairs that depend on the available payload size.
	LoadPermissive LoadFlags = 0x80
	// LoadIgnoreMips ignores mip levels for non-array input, as supported upstream.
	LoadIgnoreMips LoadFlags = 0x100
)

const (
	// HeaderDefault preserves the original file for normalized legacy input;
	// otherwise it writes DX10 with alpha metadata.
	HeaderDefault HeaderMode = iota
	// HeaderLegacy requires a DX9 header and errors if the texture cannot be represented.
	// Non-legacy metadata such as straight/opaque alpha intent cannot be preserved.
	HeaderLegacy
	// HeaderAuto lets DirectXTex choose a legacy header when representable, with
	// the same alpha-metadata limitations as HeaderLegacy. Prefer HeaderDefault
	// or HeaderDX10 when alpha association must survive serialization.
	HeaderAuto
	// HeaderDX10 always writes normalized DXGI pixels and all supported metadata.
	HeaderDX10
)

const (
	FilterWrapU         FilterFlags = 0x1
	FilterWrapV         FilterFlags = 0x2
	FilterWrapW         FilterFlags = 0x4
	FilterMirrorU       FilterFlags = 0x10
	FilterMirrorV       FilterFlags = 0x20
	FilterMirrorW       FilterFlags = 0x40
	FilterSeparateAlpha FilterFlags = 0x100
	// FilterFloatX2Bias explicitly enables UNORM/float signed-range mapping.
	FilterFloatX2Bias     FilterFlags = 0x200
	FilterCopyRed         FilterFlags = 0x1000
	FilterCopyGreen       FilterFlags = 0x2000
	FilterCopyBlue        FilterFlags = 0x4000
	FilterCopyAlpha       FilterFlags = 0x8000
	FilterDither          FilterFlags = 0x10000
	FilterDitherDiffusion FilterFlags = 0x20000
	FilterPoint           FilterFlags = 0x100000
	FilterLinear          FilterFlags = 0x200000
	FilterCubic           FilterFlags = 0x300000
	FilterBox             FilterFlags = 0x400000
	FilterTriangle        FilterFlags = 0x500000
	// FilterSRGBIn decodes sRGB before processing; sRGB formats imply it automatically.
	FilterSRGBIn FilterFlags = 0x1000000
	// FilterSRGBOut encodes sRGB after processing; sRGB formats imply it automatically.
	FilterSRGBOut FilterFlags = 0x2000000
)

const (
	CompressRGBDither   CompressionFlags = 0x10000
	CompressAlphaDither CompressionFlags = 0x20000
	CompressUniform     CompressionFlags = 0x40000
	CompressSRGBIn      CompressionFlags = 0x1000000
	CompressSRGBOut     CompressionFlags = 0x2000000
)

const (
	// BC7Default uses upstream's default mode search.
	BC7Default BC7Effort = iota
	// BC7Quick searches only mode 6, reducing effort and usually fidelity.
	BC7Quick
	// BC7Exhaustive additionally searches the expensive three-subset modes 0 and 2.
	BC7Exhaustive
)

// LoadFlags controls upstream DDS compatibility behavior. Zero is strict parsing.
type LoadFlags uint32

// HeaderMode selects legacy or DX10 serialization; it never recompresses pixels.
type HeaderMode uint32

// FilterFlags combines addressing, channel, dithering and color-space controls.
// Choose at most one FilterPoint/Linear/Cubic/Box/Triangle mode; these are encoded
// values, not independent bits. Wrap and mirror conflict on the same axis.
// Conversion and filtering support depend on the source and destination formats.
type FilterFlags uint32

// CompressionFlags selects the CPU encoder's supported weighting and color flags.
// sRGB conversion is opt-in for linear formats and implied by sRGB formats.
type CompressionFlags uint32

// BC7Effort changes search effort, not the fixed 16-byte BC7 block size.
type BC7Effort uint32

// LoadOptions controls legacy normalization and the input/native-payload budget.
// Zero MaxBytes means 512 MiB. Dimensions are bounded at 32768 and subresources
// at 65536 independently of this budget. Flags default to upstream strict parsing.
type LoadOptions struct {
	Flags    LoadFlags
	MaxBytes int64
}

type SaveOptions struct {
	Header HeaderMode
}

// ConversionOptions controls DirectXTex conversion. AlphaThreshold defaults to
// 0.5; a non-nil pointer permits an explicit zero. Values must be in [0,1].
// Filter defaults to upstream behavior, including format-implied sRGB handling.
type ConversionOptions struct {
	Filter         FilterFlags
	AlphaThreshold *float32
}

// CompressionOptions controls the CPU compressor. There is no quality/size scale:
// BC blocks have fixed sizes and encoding is lossy. BC7Effort affects only BC7.
// Dither and uniform weighting affect BC1-3; AlphaThreshold affects only BC1.
// Compression is single-threaded; callers can bound parallel independent jobs.
// No OpenMP runtime is required. BC6H accepts native signed/HDR float input.
type CompressionOptions struct {
	Flags          CompressionFlags
	BC7Effort      BC7Effort
	AlphaThreshold *float32
}

// MipmapOptions generates Levels total levels (including base); zero means a full
// chain. Except for a one-level result (which only selects base subresources),
// compressed/typeless inputs require explicit decompression/interpretation.
// Filter zero selects box for power-of-two axes, otherwise linear for 2D or
// triangle for 3D. Cubes are filtered per face, without cross-face seam filtering.
type MipmapOptions struct {
	Levels int
	Filter FilterFlags
}

func (flags FilterFlags) validate() error {
	mode := flags & 0xf00000
	dither := flags & 0xf0000
	conflict := flags & 7 & ((flags >> 4) & 7)

	if flags & ^FilterFlags(0x0373f377) != 0 || mode > FilterTriangle || dither == 0x30000 || conflict != 0 {
		return fmt.Errorf("dds: invalid or conflicting filter flags 0x%x", flags)
	}

	return nil
}

func thresholdValue(value *float32) (float32, error) {
	if value == nil {
		return 0.5, nil
	}

	if math.IsNaN(float64(*value)) || *value < 0 || *value > 1 {
		return 0, fmt.Errorf("dds: alpha threshold must be in [0,1]")
	}

	return *value, nil
}

func loadSettings(options *LoadOptions) (LoadFlags, int64, error) {
	flags := LoadFlags(0)
	limit := int64(defaultMaxBytes)

	if options != nil {
		flags = options.Flags

		if options.MaxBytes != 0 {
			limit = options.MaxBytes
		}
	}

	if flags & ^LoadFlags(0x1ff) != 0 || limit < 148 || limit > math.MaxInt32 {
		return 0, 0, fmt.Errorf("dds: invalid load flags or MaxBytes (must be 148..2147483647)")
	}

	return flags, limit, nil
}
