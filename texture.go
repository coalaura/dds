package dds

import (
	"fmt"
	"io"
	"math/bits"
)

// Texture is an immutable complete texture with Go-owned storage. Methods are
// safe for concurrent calls. Surface returns a copy; processing returns a new
// texture. The zero value is invalid. Native temporaries are freed before return.
type Texture struct {
	metadata Metadata
	surfaces []Subresource
	pixels   []byte
	original []byte
}

func (texture *Texture) Metadata() Metadata {
	return texture.metadata
}

func (texture *Texture) SubresourceCount() int {
	return len(texture.surfaces)
}

// Surface returns an independent copy of a selected native surface.
func (texture *Texture) Surface(selection Selection) (Subresource, error) {
	index, err := texture.surfaceIndex(selection)
	if err != nil {
		return Subresource{}, err
	}

	surface := texture.surfaces[index]
	surface.Pixels = append([]byte(nil), texture.pixels[surface.offset:surface.offset+surface.SlicePitch]...)
	surface.offset = 0

	return surface, nil
}

// Save writes native payloads without conversion or recompression. Default saves
// retain the original legacy file when normalization was required during loading;
// otherwise DirectXTex may normalize headers. HeaderDX10 requests normalized storage.
func (texture *Texture) Save(writer io.Writer, options *SaveOptions) error {
	if writer == nil || texture == nil || len(texture.surfaces) == 0 {
		return fmt.Errorf("dds: save requires a texture and writer")
	}

	header := HeaderDefault

	if options != nil {
		header = options.Header
	}

	if len(texture.original) != 0 && (header == HeaderDefault || header == HeaderLegacy || header == HeaderAuto) {
		count, err := writer.Write(texture.original)
		if err == nil && count != len(texture.original) {
			return io.ErrShortWrite
		}

		return err
	}

	flags := uint32(0x30000)

	switch header {
	case HeaderDefault, HeaderDX10:
	case HeaderLegacy:
		flags = 0x40000
	case HeaderAuto:
		flags = 0
	default:
		return fmt.Errorf("dds: invalid header mode %d", header)
	}

	_, err := nativeProcess(texture, 0, 0, flags, 0, 0, writer)
	return err
}

// Interpret gives a typeless texture an explicit compatible typed interpretation.
// It shares immutable storage, does not convert samples, and rejects cross-family casts.
func (texture *Texture) Interpret(format Format) (*Texture, error) {
	if texture == nil || !texture.metadata.Format.Info().Typeless || !format.Info().Valid || format.Info().Typeless || !compatibleFormats(texture.metadata.Format, format) {
		return nil, fmt.Errorf("dds: incompatible typeless interpretation")
	}

	result := *texture
	result.metadata.Format = format
	result.original = nil

	return &result, nil
}

// OriginalDDS returns a copy of the original file when loading required legacy
// normalization (for example, 24-bit RGB expansion or channel-mask swizzling).
// Otherwise it returns nil: Surface already exposes the original native payload.
// Save preserves this file unless HeaderDX10 explicitly requests normalization.
func (texture *Texture) OriginalDDS() []byte {
	return append([]byte(nil), texture.original...)
}

// Convert converts uncompressed native samples. Float/HDR and signed values are
// retained when the destination permits them. Conversion to UNORM explicitly
// clamps/maps values according to DirectXTex; it does not perform tone mapping.
// Transfer functions apply to stored RGB, independently of alpha metadata. Thus
// premultiplied sRGB uses sRGB(alpha * linear RGB). For encoded-associated input,
// first call WithAlphaMode(AlphaStraight, true). Same-format conversion is a no-op
// only with zero options; it cannot request a transfer-only change.
func (texture *Texture) Convert(format Format, options *ConversionOptions) (*Texture, error) {
	err := texture.processingCheck(format, false)
	if err != nil {
		return nil, err
	}

	settings := ConversionOptions{}

	if options != nil {
		settings = *options
	}

	err = settings.Filter.validate()
	if err != nil {
		return nil, err
	}

	threshold, err := thresholdValue(settings.AlphaThreshold)
	if err != nil {
		return nil, err
	}

	if format == texture.metadata.Format {
		if settings.Filter != 0 || settings.AlphaThreshold != nil {
			return nil, fmt.Errorf("dds: conversion options require a different output format")
		}

		return texture, nil
	}

	return nativeProcess(texture, 1, uint32(format), uint32(settings.Filter), threshold, 0, nil)
}

// Compress encodes native samples using the same format-driven transfer and alpha
// convention as Convert. It does not associate/unassociate or discard hidden RGB.
func (texture *Texture) Compress(format Format, options *CompressionOptions) (*Texture, error) {
	err := texture.processingCheck(format, true)
	if err != nil {
		return nil, err
	}

	settings := CompressionOptions{}

	if options != nil {
		settings = *options
	}

	threshold, err := thresholdValue(settings.AlphaThreshold)
	if err != nil {
		return nil, err
	}

	bc1 := format == FormatBC1_UNORM || format == FormatBC1_UNORM_SRGB
	bc123 := format >= FormatBC1_UNORM && format <= FormatBC3_UNORM_SRGB
	bc7 := format == FormatBC7_UNORM || format == FormatBC7_UNORM_SRGB

	if settings.Flags & ^CompressionFlags(0x3070000) != 0 || settings.BC7Effort > BC7Exhaustive || (!bc7 && settings.BC7Effort != BC7Default) || (!bc1 && settings.AlphaThreshold != nil) || (!bc123 && settings.Flags&0x70000 != 0) {
		return nil, fmt.Errorf("dds: incompatible compression options for format %d", format)
	}

	flags := uint32(settings.Flags)

	switch settings.BC7Effort {
	case BC7Quick:
		flags |= 0x100000
	case BC7Exhaustive:
		flags |= 0x80000
	}

	return nativeProcess(texture, 2, uint32(format), flags, threshold, 0, nil)
}

// Decompress expands BC data. FormatUNKNOWN selects a precision-preserving default:
// RGBA8 for BC1-3/7, R/RG UNORM or SNORM for BC4/5, and RGBA16 float for BC6H.
// The default retains sRGB encoding; an explicit output format follows Convert's
// per-channel transfer semantics, without changing alpha association metadata.
func (texture *Texture) Decompress(format Format) (*Texture, error) {
	if texture == nil || len(texture.surfaces) == 0 || !texture.metadata.Format.Info().Compressed || texture.metadata.Format.Info().Typeless {
		return nil, fmt.Errorf("dds: decompress requires a typed BC texture")
	}

	if format == FormatUNKNOWN {
		format = decompressedFormat(texture.metadata.Format)
	}

	info := format.Info()
	if !info.Valid || info.Typeless || info.Compressed {
		return nil, fmt.Errorf("dds: invalid decompression output format %d", format)
	}

	err := texture.outputCheck(format, texture.metadata.MipLevels)
	if err != nil {
		return nil, err
	}

	return nativeProcess(texture, 3, uint32(format), 0, 0, 0, nil)
}

// GenerateMipmaps replaces the mip chain. A one-level result retains only base
// subresources, or returns texture itself if it already has one level.
// Declared straight-alpha color is filtered in linear premultiplied space unless
// FilterSeparateAlpha explicitly requests independent channel filtering. Unknown,
// custom and opaque alpha use independent channels. Premultiplied input must use
// linear association (sRGB(alpha * linear RGB) for sRGB storage). Native signed/HDR
// channels are not clamped. Base samples and alpha metadata are preserved.
func (texture *Texture) GenerateMipmaps(options *MipmapOptions) (*Texture, error) {
	if texture == nil || len(texture.surfaces) == 0 {
		return nil, fmt.Errorf("dds: invalid texture")
	}

	settings := MipmapOptions{}

	if options != nil {
		settings = *options
	}

	err := settings.Filter.validate()
	if err != nil {
		return nil, err
	}

	levels := settings.Levels
	if levels == 0 {
		levels = bits.Len(uint(max(texture.metadata.Width, texture.metadata.Height, texture.metadata.Depth)))
	}

	err = texture.outputCheck(texture.metadata.Format, levels)
	if err != nil {
		return nil, err
	}

	if levels == 1 {
		if texture.metadata.MipLevels == 1 {
			return texture, nil
		}

		metadata := texture.metadata
		metadata.MipLevels = 1
		surfaces := make([]Subresource, 0, metadata.ArraySize*metadata.Depth)

		for item := range metadata.ArraySize {
			for slice := range metadata.Depth {
				index, indexErr := texture.surfaceIndex(Selection{Item: item, Slice: slice})
				if indexErr != nil {
					return nil, indexErr
				}

				surface := texture.surfaces[index]
				surface.Pixels = texture.pixels[surface.offset : surface.offset+surface.SlicePitch]
				surfaces = append(surfaces, surface)
			}
		}

		return NewTexture(metadata, surfaces)
	}

	err = texture.processingCheck(texture.metadata.Format, false)
	if err != nil {
		return nil, err
	}

	if texture.metadata.AlphaMode() == AlphaStraight && texture.metadata.Format.Info().Alpha && settings.Filter&FilterSeparateAlpha == 0 {
		return texture.alphaMipmaps(settings, levels)
	}

	return nativeProcess(texture, 4, 0, uint32(settings.Filter), 0, levels, nil)
}

func (texture *Texture) alphaMipmaps(settings MipmapOptions, levels int) (*Texture, error) {
	err := texture.outputCheck(FormatR32G32B32A32_FLOAT, levels)
	if err != nil {
		return nil, err
	}

	working, err := floatCopy(texture, settings.Filter&FilterSRGBIn)
	if err != nil {
		return nil, err
	}

	transformColor(working, false, false, AlphaStraight, AlphaPremultiplied)
	filter := settings.Filter &^ (FilterSRGBIn | FilterSRGBOut)

	working, err = nativeProcess(working, 4, 0, uint32(filter), 0, levels, nil)
	if err != nil {
		return nil, err
	}

	transformColor(working, false, false, AlphaPremultiplied, AlphaStraight)

	outputFilter := settings.Filter & FilterSRGBOut
	if texture.metadata.Format == FormatR32G32B32A32_FLOAT && outputFilter != 0 {
		transformColor(working, false, true, AlphaStraight, AlphaStraight)
		outputFilter = 0
	}

	result, err := working.Convert(texture.metadata.Format, &ConversionOptions{Filter: outputFilter})
	if err != nil {
		return nil, err
	}

	// Conversion round trips must not alter the retained base levels, including
	// hidden RGB at zero alpha. Only newly generated levels require processing.
	for item := range texture.metadata.ArraySize {
		for slice := range texture.metadata.Depth {
			selection := Selection{Item: item, Slice: slice}
			sourceIndex, _ := texture.surfaceIndex(selection)
			targetIndex, _ := result.surfaceIndex(selection)
			source := texture.surfaces[sourceIndex]
			target := result.surfaces[targetIndex]

			for row := range source.Height {
				copy(result.pixels[target.offset+row*target.RowPitch:target.offset+(row+1)*target.RowPitch], texture.pixels[source.offset+row*source.RowPitch:source.offset+(row+1)*source.RowPitch])
			}
		}
	}

	return result, nil
}

// WithAlphaMode premultiplies or unpremultiplies samples using DirectXTex.
// Only straight <-> premultiplied transitions are supported. ignoreSRGB controls
// whether multiplication takes place on encoded samples rather than linear light.
// For sRGB, false produces/expects sRGB(alpha * linear RGB), whereas true
// produces/expects alpha * sRGB(RGB). DDS cannot record this distinction: Image
// defaults to the former; DecodeOptions.Premultiplication selects the latter.
// No distinction exists for linear formats. All subresources are processed.
func (texture *Texture) WithAlphaMode(mode AlphaMode, ignoreSRGB bool) (*Texture, error) {
	if texture == nil {
		return nil, fmt.Errorf("dds: nil texture")
	}

	err := texture.processingCheck(texture.metadata.Format, false)
	if err != nil {
		return nil, err
	}

	previous := texture.metadata.AlphaMode()
	if previous == mode {
		return texture, nil
	}

	validTransition := (previous == AlphaStraight && mode == AlphaPremultiplied) || (previous == AlphaPremultiplied && mode == AlphaStraight)
	if !validTransition {
		return nil, fmt.Errorf("dds: alpha conversion requires straight or premultiplied metadata")
	}

	flags := uint32(0)

	if ignoreSRGB {
		flags |= 1
	}

	if mode == AlphaStraight {
		flags |= 2
	}

	return nativeProcess(texture, 5, 0, flags, 0, 0, nil)
}

func (texture *Texture) surfaceIndex(selection Selection) (int, error) {
	if texture == nil || len(texture.surfaces) == 0 {
		return 0, fmt.Errorf("dds: invalid texture")
	}

	metadata := texture.metadata
	if selection.Mip < 0 || selection.Mip >= metadata.MipLevels || selection.Item < 0 || selection.Item >= metadata.ArraySize || selection.Slice < 0 || selection.Slice >= max(1, metadata.Depth>>selection.Mip) {
		return 0, fmt.Errorf("dds: surface selection out of range: %+v", selection)
	}

	if metadata.Dimension != Dimension3D {
		return selection.Item*metadata.MipLevels + selection.Mip, nil
	}

	index := selection.Slice

	for mip := 0; mip < selection.Mip; mip++ {
		index += max(1, metadata.Depth>>mip)
	}

	return index, nil
}

func (texture *Texture) processingCheck(format Format, compressed bool) error {
	if texture == nil || len(texture.surfaces) == 0 {
		return fmt.Errorf("dds: invalid texture")
	}

	source := texture.metadata.Format.Info()
	target := format.Info()

	if source.Typeless || target.Typeless {
		return fmt.Errorf("dds: processing typeless data requires an explicit compatible interpretation")
	}

	if source.Compressed || !target.Valid || target.Compressed != compressed {
		return fmt.Errorf("dds: incompatible formats for operation; decompress BC input first")
	}

	return texture.outputCheck(format, texture.metadata.MipLevels)
}

func (texture *Texture) outputCheck(format Format, levels int) error {
	metadata := texture.metadata
	metadata.Format = format
	metadata.MipLevels = levels
	_, _, err := layout(metadata, defaultMaxBytes)
	return err
}

// NewTexture copies native subresources in DDS order: item, mip, then depth slice.
// For volumes there is one item. Pitches and lengths are checked before native use.
// All metadata dimensions/counts must be explicit (one, not zero, for absent axes).
func NewTexture(metadata Metadata, surfaces []Subresource) (*Texture, error) {
	expected, _, err := layout(metadata, defaultMaxBytes)
	if err != nil {
		return nil, err
	}

	if len(surfaces) != len(expected) {
		return nil, fmt.Errorf("dds: got %d subresources, need %d", len(surfaces), len(expected))
	}

	total := 0

	for index := range surfaces {
		surface := &surfaces[index]
		minimum := &expected[index]
		rows := (minimum.SlicePitch + minimum.RowPitch - 1) / minimum.RowPitch

		if surface.Width != minimum.Width || surface.Height != minimum.Height || surface.RowPitch < minimum.RowPitch || surface.RowPitch > defaultMaxBytes || surface.SlicePitch < surface.RowPitch*rows || surface.SlicePitch > defaultMaxBytes-total || len(surface.Pixels) != surface.SlicePitch {
			return nil, fmt.Errorf("dds: invalid dimensions, pitches or payload for subresource %d", index)
		}

		total += surface.SlicePitch
	}

	texture := &Texture{metadata: metadata, surfaces: expected, pixels: make([]byte, total)}
	offset := 0

	for index := range surfaces {
		surface := surfaces[index]
		copy(texture.pixels[offset:], surface.Pixels)
		surface.Pixels = nil
		surface.offset = offset
		texture.surfaces[index] = surface
		offset += surface.SlicePitch
	}

	return texture, nil
}

func layout(metadata Metadata, limit int64) ([]Subresource, int, error) {
	validDimensions := metadata.Width > 0 && metadata.Height > 0 && metadata.Depth > 0 && max(metadata.Width, metadata.Height, metadata.Depth) <= maxDimension
	validCounts := metadata.ArraySize > 0 && metadata.ArraySize <= maxSubresources && metadata.MipLevels > 0 && metadata.MipLevels <= bits.Len(uint(max(metadata.Width, metadata.Height, metadata.Depth)))

	if !validDimensions || !validCounts || !metadata.Format.Info().Valid || metadata.Format == FormatUNKNOWN || metadata.AlphaMode() > AlphaCustom {
		return nil, 0, fmt.Errorf("dds: invalid metadata dimensions, counts, format or alpha mode")
	}

	if metadata.Dimension < Dimension1D || metadata.Dimension > Dimension3D || (metadata.Dimension == Dimension1D && metadata.Height != 1) || (metadata.Dimension != Dimension3D && metadata.Depth != 1) || (metadata.Dimension == Dimension3D && metadata.ArraySize != 1) {
		return nil, 0, fmt.Errorf("dds: incompatible resource dimension")
	}

	if metadata.IsCube() && (metadata.Dimension != Dimension2D || metadata.ArraySize%6 != 0 || metadata.Width != metadata.Height) {
		return nil, 0, fmt.Errorf("dds: cube textures require square 2D faces in multiples of six")
	}

	count := 0

	for mip := 0; mip < metadata.MipLevels; mip++ {
		count += metadata.ArraySize * max(1, metadata.Depth>>mip)
	}

	if count > maxSubresources {
		return nil, 0, fmt.Errorf("dds: too many subresources")
	}

	surfaces := make([]Subresource, 0, count)
	total := 0

	for item := 0; item < metadata.ArraySize; item++ {
		for mip := 0; mip < metadata.MipLevels; mip++ {
			width := max(1, metadata.Width>>mip)
			height := max(1, metadata.Height>>mip)

			row, slice, err := metadata.Format.Pitch(width, height)
			if err != nil {
				return nil, 0, err
			}

			for depth := 0; depth < max(1, metadata.Depth>>mip); depth++ {
				if row <= 0 || slice <= 0 || int64(slice) > limit-int64(total) {
					return nil, 0, fmt.Errorf("dds: texture exceeds payload budget")
				}

				surfaces = append(surfaces, Subresource{Width: width, Height: height, RowPitch: row, SlicePitch: slice, offset: total})
				total += slice
			}
		}
	}

	return surfaces, total, nil
}

func decompressedFormat(format Format) Format {
	switch format {
	case FormatBC4_UNORM:
		return FormatR8_UNORM
	case FormatBC4_SNORM:
		return FormatR8_SNORM
	case FormatBC5_UNORM:
		return FormatR8G8_UNORM
	case FormatBC5_SNORM:
		return FormatR8G8_SNORM
	case FormatBC6H_UF16, FormatBC6H_SF16:
		return FormatR16G16B16A16_FLOAT
	case FormatBC1_UNORM_SRGB, FormatBC2_UNORM_SRGB, FormatBC3_UNORM_SRGB, FormatBC7_UNORM_SRGB:
		return FormatR8G8B8A8_UNORM_SRGB
	default:
		return FormatR8G8B8A8_UNORM
	}
}
