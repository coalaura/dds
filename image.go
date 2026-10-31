package dds

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
)

const (
	PrecisionAuto ImagePrecision = iota
	Precision8
	Precision16
)

const (
	// DisplayStrict refuses float, signed, and integer data that has no faithful Go image model.
	DisplayStrict DisplayMode = iota
	// DisplayClamp explicitly converts such data to UNORM using DirectXTex's rules.
	// Float values are clamped to [0,1]; SNORM is mapped from [-1,1] to [0,1].
	// Integer formats use upstream integer-to-normalized conversion. No tone mapping
	// or normal-map channel reconstruction is performed.
	DisplayClamp
)

type ImagePrecision uint32
type DisplayMode uint32

// DecodeOptions selects one surface and its Go image conversion.
// Interpretation is required for typeless data and otherwise must be UNKNOWN.
// PrecisionAuto uses 16-bit samples when native precision exceeds eight bits.
// Unknown alpha is treated as straight; custom alpha requires AlphaInterpretation.
// AlphaInterpretation may explicitly declare straight or premultiplied samples.
// Filter follows DirectXTex conversion semantics; no filtering is done when the
// selected surface already has the requested Go layout and Filter is zero.
type DecodeOptions struct {
	Surface             Selection
	Interpretation      Format
	Precision           ImagePrecision
	Display             DisplayMode
	AlphaInterpretation AlphaMode
	Filter              FilterFlags
	Load                LoadOptions
}

// Options controls image encoding. Nil options preserve source precision and alpha
// in an uncompressed DX10 DDS. FormatUNKNOWN selects RGBA8 or RGBA16 UNORM.
// Format selects storage explicitly, including BC1-7. AlphaUnknown retains
// the input alpha convention; Straight/Premultiplied convert samples explicitly.
// Compression applies only to BC output, Conversion only to uncompressed output.
// Mipmaps, when non-nil, are generated before output conversion/compression.
type Options struct {
	Format      Format
	Header      HeaderMode
	AlphaMode   AlphaMode
	Compression *CompressionOptions
	Conversion  *ConversionOptions
	Mipmaps     *MipmapOptions
}

func init() {
	image.RegisterFormat("dds", "DDS ", Decode, DecodeConfig)
}

// Image converts one selected surface without discarding this texture's other data.
// The returned image owns its pixels independently of the texture.
func (texture *Texture) Image(options *DecodeOptions) (image.Image, error) {
	settings := DecodeOptions{}

	if options != nil {
		settings = *options
	}

	index, err := texture.surfaceIndex(settings.Surface)
	if err != nil {
		return nil, err
	}

	if settings.Interpretation != FormatUNKNOWN {
		texture, err = texture.Interpret(settings.Interpretation)
		if err != nil {
			return nil, err
		}
	}

	info := texture.metadata.Format.Info()

	err = imageSettings(info, settings)
	if err != nil {
		return nil, err
	}

	surface := texture.surfaces[index]
	metadata := texture.metadata
	metadata.Width = surface.Width
	metadata.Height = surface.Height
	metadata.Depth = 1
	metadata.ArraySize = 1
	metadata.MipLevels = 1
	metadata.Dimension = Dimension2D
	metadata.MiscFlags &^= 4
	selected := &Texture{metadata: metadata, surfaces: []Subresource{surface}, pixels: texture.pixels}

	if info.Compressed {
		selected, err = selected.Decompress(FormatUNKNOWN)
		if err != nil {
			return nil, err
		}
	}

	wide := settings.Precision == Precision16 || (settings.Precision == PrecisionAuto && info.BitsPerColor > 8)
	target := FormatR8G8B8A8_UNORM

	if wide {
		target = FormatR16G16B16A16_UNORM
	} else if info.SRGB {
		target = FormatR8G8B8A8_UNORM_SRGB
	}

	filter := settings.Filter
	// RGBA16 has no sRGB variant; preserve encoded sample values for Go images.
	if wide && info.SRGB {
		filter |= FilterSRGBOut
	}

	if selected.metadata.Format != target || filter != 0 {
		selected, err = selected.Convert(target, &ConversionOptions{Filter: filter})
		if err != nil {
			return nil, err
		}
	}

	alpha := metadata.AlphaMode()

	if settings.AlphaInterpretation != AlphaUnknown {
		alpha = settings.AlphaInterpretation
	}

	if alpha == AlphaCustom {
		return nil, fmt.Errorf("dds: custom alpha needs an explicit AlphaInterpretation")
	}

	return selected.goImage(wide, alpha == AlphaPremultiplied), nil
}

func (texture *Texture) goImage(wide, premultiplied bool) image.Image {
	surface := texture.surfaces[0]
	bounds := image.Rect(0, 0, surface.Width, surface.Height)
	stride := surface.Width * 4

	if wide {
		stride *= 2
	}

	pixels := make([]byte, stride*surface.Height)

	for row := 0; row < surface.Height; row++ {
		source := texture.pixels[surface.offset+row*surface.RowPitch : surface.offset+row*surface.RowPitch+stride]
		target := pixels[row*stride : (row+1)*stride]

		if wide {
			for offset := 0; offset < stride; offset += 2 {
				target[offset] = source[offset+1]
				target[offset+1] = source[offset]
			}
		} else {
			copy(target, source)
		}
	}

	if wide {
		if premultiplied {
			return &image.RGBA64{Pix: pixels, Stride: stride, Rect: bounds}
		}

		return &image.NRGBA64{Pix: pixels, Stride: stride, Rect: bounds}
	}

	if premultiplied {
		return &image.RGBA{Pix: pixels, Stride: stride, Rect: bounds}
	}

	return &image.NRGBA{Pix: pixels, Stride: stride, Rect: bounds}
}

// Decode selects base mip, first array element/cube face, and first volume slice.
// Signed/HDR/integer data requires DecodeWithOptions or the native Texture API.
func Decode(reader io.Reader) (image.Image, error) {
	return DecodeWithOptions(reader, nil)
}

func DecodeWithOptions(reader io.Reader, options *DecodeOptions) (image.Image, error) {
	var load *LoadOptions

	if options != nil {
		load = &options.Load
	}

	texture, err := Load(reader, load)
	if err != nil {
		return nil, err
	}

	return texture.Image(options)
}

// DecodeConfig inspects only the header. It reports the default surface and color
// model, or errors if the default Go image conversion would discard sample range.
func DecodeConfig(reader io.Reader) (image.Config, error) {
	metadata, err := Inspect(reader, nil)
	if err != nil {
		return image.Config{}, err
	}

	info := metadata.Format.Info()

	err = imageSettings(info, DecodeOptions{})
	if err != nil {
		return image.Config{}, err
	}

	if metadata.AlphaMode() == AlphaCustom {
		return image.Config{}, fmt.Errorf("dds: custom alpha needs explicit interpretation")
	}

	model := color.NRGBAModel

	if info.BitsPerColor > 8 {
		model = color.NRGBA64Model
	}

	if metadata.AlphaMode() == AlphaPremultiplied {
		model = color.RGBAModel

		if info.BitsPerColor > 8 {
			model = color.RGBA64Model
		}
	}

	return image.Config{ColorModel: model, Width: metadata.Width, Height: metadata.Height}, nil
}

func Encode(writer io.Writer, source image.Image, options *Options) error {
	settings := Options{}

	if options != nil {
		settings = *options
	}

	if writer == nil {
		return fmt.Errorf("dds: nil writer")
	}

	texture, err := FromImage(source)
	if err != nil {
		return err
	}

	if settings.AlphaMode != AlphaUnknown {
		texture, err = texture.WithAlphaMode(settings.AlphaMode, true)
		if err != nil {
			return err
		}
	}

	if settings.Mipmaps != nil {
		texture, err = texture.GenerateMipmaps(settings.Mipmaps)
		if err != nil {
			return err
		}
	}

	format := settings.Format
	if format == FormatUNKNOWN {
		format = texture.metadata.Format
	}

	if format.Info().Compressed {
		if settings.Conversion != nil {
			return fmt.Errorf("dds: Conversion options require uncompressed output")
		}

		texture, err = texture.Compress(format, settings.Compression)
	} else {
		if settings.Compression != nil {
			return fmt.Errorf("dds: Compression options require BC output")
		}

		texture, err = texture.Convert(format, settings.Conversion)
	}

	if err != nil {
		return err
	}

	return texture.Save(writer, &SaveOptions{Header: settings.Header})
}

// FromImage copies an ordinary image, preserving RGBA premultiplication and 16-bit
// precision. Generic images and palettes use 16-bit samples to avoid guessing their
// precision. Grayscale is explicitly replicated into RGB; alpha-only images use white.
func FromImage(source image.Image) (*Texture, error) {
	if source == nil {
		return nil, fmt.Errorf("dds: nil image")
	}

	bounds := source.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	wide := true
	premultiplied := false

	switch source.(type) {
	case *image.NRGBA, *image.Gray, *image.Alpha, *image.YCbCr, *image.CMYK:
		wide = false
	case *image.RGBA:
		wide = false
		premultiplied = true
	case *image.RGBA64:
		premultiplied = true
	}

	format := FormatR8G8B8A8_UNORM

	if wide {
		format = FormatR16G16B16A16_UNORM
	}

	alpha := AlphaStraight

	if premultiplied {
		alpha = AlphaPremultiplied
	}

	metadata := Metadata{Width: width, Height: height, Depth: 1, ArraySize: 1, MipLevels: 1, Format: format, Dimension: Dimension2D, MiscFlags2: uint32(alpha)}

	surfaces, size, err := layout(metadata, defaultMaxBytes)
	if err != nil {
		return nil, err
	}

	texture := &Texture{metadata: metadata, surfaces: surfaces, pixels: make([]byte, size)}
	if copyImageRows(texture, source) {
		return texture, nil
	}

	for row := range height {
		for column := range width {
			pixel := source.At(bounds.Min.X+column, bounds.Min.Y+row)

			if wide {
				converted := color.NRGBA64Model.Convert(pixel).(color.NRGBA64)

				if premultiplied {
					red, green, blue, alpha := pixel.RGBA()
					converted = color.NRGBA64{R: uint16(red), G: uint16(green), B: uint16(blue), A: uint16(alpha)}
				}

				offset := row*surfaces[0].RowPitch + column*8
				binary.LittleEndian.PutUint16(texture.pixels[offset:], converted.R)
				binary.LittleEndian.PutUint16(texture.pixels[offset+2:], converted.G)
				binary.LittleEndian.PutUint16(texture.pixels[offset+4:], converted.B)
				binary.LittleEndian.PutUint16(texture.pixels[offset+6:], converted.A)
			} else {
				converted := color.NRGBAModel.Convert(pixel).(color.NRGBA)

				if premultiplied {
					red, green, blue, alpha := pixel.RGBA()
					converted = color.NRGBA{R: uint8(red >> 8), G: uint8(green >> 8), B: uint8(blue >> 8), A: uint8(alpha >> 8)}
				}

				offset := row*surfaces[0].RowPitch + column*4
				texture.pixels[offset] = converted.R
				texture.pixels[offset+1] = converted.G
				texture.pixels[offset+2] = converted.B
				texture.pixels[offset+3] = converted.A
			}
		}
	}

	return texture, nil
}

func imageSettings(info FormatInfo, settings DecodeOptions) error {
	if settings.Precision > Precision16 || settings.Display > DisplayClamp || (settings.AlphaInterpretation != AlphaUnknown && settings.AlphaInterpretation != AlphaStraight && settings.AlphaInterpretation != AlphaPremultiplied) {
		return fmt.Errorf("dds: invalid image conversion options")
	}

	if info.Typeless {
		return fmt.Errorf("dds: typeless pixels require an explicit compatible interpretation")
	}

	if settings.Display == DisplayStrict && info.DataType != TypeUNorm {
		return fmt.Errorf("dds: signed, float and integer samples require explicit DisplayClamp or the Texture API")
	}

	return settings.Filter.validate()
}

func copyImageRows(texture *Texture, source image.Image) bool {
	var (
		pixels []byte
		stride int
		wide   = false
	)

	switch source := source.(type) {
	case *image.NRGBA:
		pixels = source.Pix
		stride = source.Stride
	case *image.RGBA:
		pixels = source.Pix
		stride = source.Stride
	case *image.NRGBA64:
		pixels = source.Pix
		stride = source.Stride
		wide = true
	case *image.RGBA64:
		pixels = source.Pix
		stride = source.Stride
		wide = true
	default:
		return false
	}

	rowPitch := texture.surfaces[0].RowPitch

	for row := range texture.metadata.Height {
		target := texture.pixels[row*rowPitch : (row+1)*rowPitch]
		input := pixels[row*stride : row*stride+rowPitch]

		if wide {
			for offset := 0; offset < rowPitch; offset += 2 {
				target[offset] = input[offset+1]
				target[offset+1] = input[offset]
			}
		} else {
			copy(target, input)
		}
	}

	return true
}
