package dds

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
)

type failingWriter struct{ err error }
type failingReader struct{ err error }
type shortWriter struct{}

type colorCase struct {
	name      string
	options   Options
	want      uint8
	tolerance uint32
}

type alphaColorCase struct {
	name      string
	wide      bool
	space     SourceColorSpace
	format    Format
	pixels    []color.NRGBA64
	mips      bool
	tolerance uint32
}

type encodingCase struct {
	name      string
	format    Format
	channels  int
	tolerance float64
	mips      bool
}

func (writer failingWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

func (reader failingReader) Read([]byte) (int, error) {
	return 0, reader.err
}

func (shortWriter) Write(data []byte) (int, error) {
	return len(data) - 1, nil
}

func TestCorpus(t *testing.T) {
	paths, err := filepath.Glob("testdata/*.dds")
	if err != nil || len(paths) < 25 {
		t.Fatalf("fixtures: %d, %v", len(paths), err)
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			check(t, err)
			texture, err := LoadAt(bytes.NewReader(data), int64(len(data)), nil)
			check(t, err)
			metadata, err := InspectAt(bytes.NewReader(data), nil)
			check(t, err)

			if metadata != texture.Metadata() {
				t.Fatal("inspection metadata differs")
			}

			var encoded bytes.Buffer

			check(t, texture.Save(&encoded, nil))
			reloaded, err := Load(&encoded, nil)
			check(t, err)

			if reloaded.Metadata() != metadata || len(reloaded.surfaces) != len(texture.surfaces) || !bytes.Equal(reloaded.pixels, texture.pixels) {
				t.Fatal("native round trip changed metadata or payload")
			}

			if metadata.Format.Info().Typeless {
				_, err = texture.Image(nil)
				if err == nil {
					t.Fatal("typeless conversion accepted without interpretation")
				}

				texture, err = texture.Interpret(FormatR8G8B8A8_UNORM)
				check(t, err)
			}

			_, err = texture.Image(&DecodeOptions{Display: DisplayClamp})
			check(t, err)
		})
	}
}

func TestIndependentReferences(t *testing.T) {
	names := []string{"legacy-DXT1", "legacy-DXT3", "legacy-DXT5", "legacy-rgb24"}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", name+".dds"))
			check(t, err)
			decoded, format, err := image.Decode(bytes.NewReader(data))
			check(t, err)
			config, configFormat, err := image.DecodeConfig(bytes.NewReader(data))
			check(t, err)

			if format != "dds" || configFormat != "dds" || config.Width != decoded.Bounds().Dx() || config.Height != decoded.Bounds().Dy() {
				t.Fatal("registration/config mismatch")
			}

			referenceData, err := os.ReadFile(filepath.Join("testdata", name+".png"))
			check(t, err)
			reference, err := png.Decode(bytes.NewReader(referenceData))
			check(t, err)
			compareImages(t, decoded, reference, 0)
		})
	}

	premultipliedNames := []string{"legacy-DXT2", "legacy-DXT4"}

	for _, name := range premultipliedNames {
		texture := fixture(t, name)
		if texture.Metadata().AlphaMode() != AlphaPremultiplied {
			t.Fatal("lost DXT2/4 premultiplication")
		}

		decoded, err := texture.Image(nil)

		if name == "legacy-DXT4" {
			// The fixture's RGB565 endpoint expands to red 132, above alpha 128.
			// Native decoding retains it; a normalized Go image needs explicit clamping.
			if err == nil {
				t.Fatal("accepted out-of-range DXT4 premultiplication")
			}

			decoded, err = texture.Image(&DecodeOptions{Display: DisplayClamp})
		}

		check(t, err)

		if _, ok := decoded.(*image.RGBA); !ok {
			t.Fatalf("premultiplied image type: %T", decoded)
		}

		red, green, blue, alpha := decoded.At(0, 0).RGBA()
		if red > alpha || green > alpha || blue > alpha {
			t.Fatal("invalid Go premultiplied color")
		}
	}

	transparent, err := fixture(t, "legacy-bc1-alpha").Image(nil)
	check(t, err)

	_, _, _, alpha := transparent.At(0, 0).RGBA()
	if alpha != 0 {
		t.Fatal("BC1 transparent selector became opaque")
	}
}

func TestPrecisionAndStructure(t *testing.T) {
	colorNames := []string{"bc1", "bc2", "bc3", "bc7", "bc7-srgb", "rgba16", "rgb565", "rgb10a2"}

	for _, name := range colorNames {
		decoded, err := fixture(t, name).Image(nil)
		check(t, err)
		pixel := color.NRGBA64Model.Convert(decoded.At(0, 0)).(color.NRGBA64)
		want := color.NRGBA64{R: 16384, G: 32768, B: 49151, A: 65535}

		if name == "bc7-srgb" {
			want = color.NRGBA64{R: 35200, G: 48192, B: 57725, A: 65535}
		}

		if math.Abs(float64(pixel.R)-float64(want.R)) > 2500 || math.Abs(float64(pixel.G)-float64(want.G)) > 2500 || math.Abs(float64(pixel.B)-float64(want.B)) > 2500 || pixel.A != want.A {
			t.Fatalf("%s: pixel %+v, want approximately %+v", name, pixel, want)
		}
	}

	names := []string{"bc4s", "bc5s", "bc6u", "bc6s", "float32"}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			texture := fixture(t, name)

			_, err := texture.Image(nil)
			if err == nil {
				t.Fatal("signed/HDR image conversion must be explicit")
			}

			if texture.Metadata().Format.Info().Compressed {
				texture, err = texture.Decompress(FormatUNKNOWN)
				check(t, err)
			}

			texture, err = texture.Convert(FormatR32G32B32A32_FLOAT, nil)
			check(t, err)
			red := math.Float32frombits(binary.LittleEndian.Uint32(texture.pixels))
			want := float32(-0.5)

			switch name {
			case "bc6u":
				want = 2
			case "bc6s", "float32":
				want = -2
			}

			if math.Abs(float64(red-want)) > 0.025 {
				t.Fatalf("red = %g, want %g", red, want)
			}
		})
	}

	cube := fixture(t, "cube-array")
	if !cube.Metadata().IsCube() || cube.Metadata().ArraySize != 12 || cube.SubresourceCount() != 36 {
		t.Fatal("cube array structure lost")
	}

	surface, err := cube.Surface(CubeSelection(1, FaceNegativeZ, 2))
	check(t, err)

	if surface.Width != 1 || surface.Pixels[0] != 176 || surface.Pixels[1] != 128 {
		t.Fatal("wrong cube face/mip")
	}

	volume := fixture(t, "volume")
	surface, err = volume.Surface(Selection{Slice: 3})
	check(t, err)

	if volume.SubresourceCount() != 7 || surface.Pixels[2] != 192 {
		t.Fatal("wrong volume layout")
	}

	_, err = volume.Surface(Selection{Mip: 2, Slice: 1})
	if err == nil {
		t.Fatal("out-of-range depth slice accepted")
	}

	base, err := FromImage(image.NewNRGBA(image.Rect(0, 0, 8, 4)))
	check(t, err)
	mips, err := base.GenerateMipmaps(nil)
	check(t, err)

	if mips.Metadata().MipLevels != 4 || mips.SubresourceCount() != 4 {
		t.Fatal("wrong generated mip chain")
	}

	premultiplied, err := base.WithAlphaMode(AlphaPremultiplied, true)
	check(t, err)

	if premultiplied.Metadata().AlphaMode() != AlphaPremultiplied {
		t.Fatal("alpha conversion lost metadata")
	}

	legacy := fixture(t, "legacy-rgb24")
	original, err := os.ReadFile("testdata/legacy-rgb24.dds")
	check(t, err)

	if !bytes.Equal(legacy.OriginalDDS(), original) {
		t.Fatal("legacy normalization lost original payload")
	}
}

func TestImageRoundTrips(t *testing.T) {
	bounds := image.Rect(7, 9, 14, 14)
	straight := image.NewNRGBA(bounds)
	premultiplied := image.NewRGBA(bounds)
	wide := image.NewNRGBA64(bounds)
	gray := image.NewGray16(bounds)
	alpha := image.NewAlpha(bounds)
	palette := color.Palette{color.NRGBA64{R: 12345, G: 23456, B: 34567, A: 45678}}
	paletted := image.NewPaletted(bounds, palette)

	for row := bounds.Min.Y; row < bounds.Max.Y; row++ {
		for column := bounds.Min.X; column < bounds.Max.X; column++ {
			straight.SetNRGBA(column, row, color.NRGBA{R: 51, G: 103, B: 205, A: 127})
			premultiplied.SetRGBA(column, row, color.RGBA{R: 31, G: 63, B: 95, A: 127})
			wide.SetNRGBA64(column, row, color.NRGBA64{R: 12345, G: 23456, B: 34567, A: 45678})
			gray.SetGray16(column, row, color.Gray16{Y: 32123})
			alpha.SetAlpha(column, row, color.Alpha{A: 127})
		}
	}

	sub := image.Rect(8, 10, 12, 13)
	images := []image.Image{straight.SubImage(sub), premultiplied.SubImage(sub), wide.SubImage(sub), gray.SubImage(sub), alpha.SubImage(sub), paletted.SubImage(sub)}

	for _, source := range images {
		var encoded bytes.Buffer

		check(t, Encode(&encoded, source, nil))
		decoded, err := Decode(&encoded)
		check(t, err)
		compareImages(t, source, decoded, 0)
	}
}

func TestInvalidInputsAndOptions(t *testing.T) {
	data, err := os.ReadFile("testdata/bc7.dds")
	check(t, err)
	lengths := []int{0, 3, 127, 128, 147, len(data) - 1}

	for _, length := range lengths {
		_, err = Load(bytes.NewReader(data[:length]), nil)
		if err == nil {
			t.Fatalf("accepted truncated input length %d", length)
		}
	}

	bad := bytes.Clone(data)
	binary.LittleEndian.PutUint32(bad[16:], math.MaxUint32)

	_, err = Load(bytes.NewReader(bad), nil)
	if err == nil {
		t.Fatal("accepted oversized dimensions")
	}

	_, err = Load(bytes.NewReader(data), &LoadOptions{MaxBytes: 148})
	if err == nil {
		t.Fatal("ignored input budget")
	}

	_, err = LoadAt(bytes.NewReader(data), int64(len(data)+1), nil)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("reader-at size mismatch: %v", err)
	}

	readFailure := errors.New("reader failure")
	_, err = Load(io.MultiReader(bytes.NewReader(data[:148]), failingReader{err: readFailure}), nil)

	if !errors.Is(err, readFailure) {
		t.Fatalf("reader error: %v", err)
	}

	_, err = Inspect(io.MultiReader(bytes.NewReader(data[:148]), failingReader{err: readFailure}), nil)
	check(t, err)

	texture := fixture(t, "bc7")

	_, err = texture.Image(&DecodeOptions{Surface: Selection{Item: 1}})
	if err == nil {
		t.Fatal("invalid selection silently fell back")
	}

	base, err := FromImage(image.NewNRGBA(image.Rect(0, 0, 4, 4)))
	check(t, err)

	_, err = base.Compress(FormatBC3_UNORM, &CompressionOptions{BC7Effort: BC7Quick})
	if err == nil {
		t.Fatal("accepted incompatible compression settings")
	}

	_, err = base.Compress(FormatBC1_UNORM, &CompressionOptions{AlphaThreshold: new(float32(math.NaN()))})
	if err == nil {
		t.Fatal("accepted NaN threshold")
	}

	_, err = base.Convert(FormatR16_UNORM, &ConversionOptions{Filter: FilterFlags(0xffffffff)})
	if err == nil {
		t.Fatal("accepted unknown filter flags")
	}

	err = texture.Save(shortWriter{}, nil)
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write: %v", err)
	}

	sentinel := errors.New("writer failure")
	err = texture.Save(failingWriter{err: sentinel}, nil)

	if !errors.Is(err, sentinel) {
		t.Fatalf("writer error: %v", err)
	}

	_, err = NewTexture(base.Metadata(), []Subresource{{Width: 4, Height: 4, RowPitch: math.MaxInt, SlicePitch: 1, Pixels: []byte{0}}})
	if err == nil {
		t.Fatal("accepted overflowing pitch")
	}
}

func TestEncodingColor(t *testing.T) {
	t.Run("association", testColorAssociation)
	t.Run("native-association", testNativeAssociation)
	t.Run("complete-texture", testCompleteColor)
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	source.SetNRGBA(0, 0, color.NRGBA{R: 128, G: 128, B: 128, A: 255})
	cases := []colorCase{
		{"default-unorm", Options{}, 128, 0},
		{"default-srgb", Options{Format: FormatR8G8B8A8_UNORM_SRGB}, 128, 0},
		{"explicit-srgb", Options{Format: FormatR8G8B8A8_UNORM_SRGB, SourceColorSpace: SourceColorSRGB, Conversion: &ConversionOptions{Filter: FilterSRGBIn | FilterSRGBOut}}, 128, 0},
		{"linear-to-srgb", Options{Format: FormatR8G8B8A8_UNORM_SRGB, SourceColorSpace: SourceColorLinear}, 188, 257},
		{"srgb-to-linear", Options{SourceColorSpace: SourceColorSRGB}, 55, 257},
		{"bc7-srgb", Options{Format: FormatBC7_UNORM_SRGB}, 128, 3 * 257},
		{"bc7-linear-to-srgb", Options{Format: FormatBC7_UNORM_SRGB, SourceColorSpace: SourceColorLinear}, 188, 3 * 257},
		{"bc7-redundant-flags", Options{Format: FormatBC7_UNORM_SRGB, Compression: &CompressionOptions{Flags: CompressSRGBIn | CompressSRGBOut}}, 128, 3 * 257},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			texture := encodedImage(t, source, &test.options)
			actual, err := texture.Image(nil)
			check(t, err)
			want := image.NewNRGBA(source.Bounds())
			want.SetNRGBA(0, 0, color.NRGBA{R: test.want, G: test.want, B: test.want, A: 255})
			compareImages(t, actual, want, test.tolerance)
		})
	}

	t.Run("gamma-mips", func(t *testing.T) {
		pattern := image.NewNRGBA(image.Rect(0, 0, 2, 1))
		pattern.SetNRGBA(0, 0, color.NRGBA{A: 255})
		pattern.SetNRGBA(1, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		texture := encodedImage(t, pattern, &Options{Format: FormatR8G8B8A8_UNORM_SRGB, Mipmaps: &MipmapOptions{Filter: FilterBox}})
		mip, err := texture.Surface(Selection{Mip: 1})
		check(t, err)

		if mip.Pixels[0] != 188 || mip.Pixels[3] != 255 {
			t.Fatalf("gamma mip: %v", mip.Pixels)
		}
	})

	t.Run("wide-color-and-alpha", func(t *testing.T) {
		wide := image.NewNRGBA64(image.Rect(0, 0, 2, 1))
		wide.SetNRGBA64(0, 0, color.NRGBA64{R: 12345, G: 23456, B: 34567, A: 45678})
		wide.SetNRGBA64(1, 0, color.NRGBA64{R: 54321, G: 43210, B: 32109, A: 45678})
		texture := encodedImage(t, wide, &Options{SourceColorSpace: SourceColorSRGB, Mipmaps: &MipmapOptions{Filter: FilterBox}})
		actual, err := texture.Image(&DecodeOptions{Surface: Selection{Mip: 1}})
		check(t, err)
		pixel := actual.(*image.NRGBA64).NRGBA64At(0, 0)
		channels := [...]uint16{pixel.R, pixel.G, pixel.B}
		first := [...]uint16{12345, 23456, 34567}
		second := [...]uint16{54321, 43210, 32109}

		for channel, value := range channels {
			want := (linearSample(first[channel]) + linearSample(second[channel])) * 0.5 * 65535
			if math.Abs(float64(value)-want) > 4 {
				t.Fatalf("wide mip channel %d: %d, want %g", channel, value, want)
			}
		}

		if texture.Metadata().Format != FormatR16G16B16A16_UNORM || pixel.A != 45678 {
			t.Fatalf("lost precision or alpha: %+v, %+v", texture.Metadata(), pixel)
		}

		premultiplied := image.NewRGBA64(wide.Bounds())
		premultiplied.SetRGBA64(0, 0, color.RGBA64{R: 12345, G: 23456, B: 34567, A: 45678})
		texture = encodedImage(t, premultiplied, nil)
		actual, err = texture.Image(nil)
		check(t, err)
		compareImages(t, actual, premultiplied, 0)

		if _, ok := actual.(*image.RGBA64); !ok {
			t.Fatalf("lost premultiplied convention: %T", actual)
		}
	})

	t.Run("conflicts", func(t *testing.T) {
		invalid := []Options{
			{SourceColorSpace: SourceColorSpace(99)},
			{SourceColorSpace: SourceColorLinear, Format: FormatR8G8B8A8_UNORM_SRGB, Conversion: &ConversionOptions{Filter: FilterSRGBIn}},
			{SourceColorSpace: SourceColorLinear, Format: FormatBC7_UNORM_SRGB, Compression: &CompressionOptions{Flags: CompressSRGBIn}},
			{SourceColorSpace: SourceColorSRGB, Conversion: &ConversionOptions{Filter: FilterSRGBOut}},
			{Format: FormatBC7_UNORM, Compression: &CompressionOptions{Flags: CompressSRGBOut}},
			{Mipmaps: &MipmapOptions{Filter: FilterSRGBIn}},
			{Mipmaps: &MipmapOptions{Filter: FilterSRGBOut}},
		}

		for _, options := range invalid {
			err := Encode(io.Discard, source, &options)
			if err == nil {
				t.Fatalf("accepted conflicting options: %+v", options)
			}
		}
	})

	native, err := FromImage(source)
	check(t, err)
	native, err = native.Convert(FormatR8G8B8A8_UNORM_SRGB, nil)
	check(t, err)

	if native.pixels[0] != 188 {
		t.Fatal("native format-driven conversion changed")
	}
}

func testColorAssociation(t *testing.T) {
	partial := []color.NRGBA64{{R: 32896, G: 32896, B: 32896, A: 32896}, {}, {R: 65535, G: 65535, B: 65535, A: 65535}}
	cases := []alphaColorCase{
		{"srgb-to-linear", false, SourceColorSRGB, FormatR8G8B8A8_UNORM, partial, false, 2 * 257},
		{"linear-to-srgb", false, SourceColorLinear, FormatR8G8B8A8_UNORM_SRGB, partial, false, 2 * 257},
		{"wide-small-alpha", true, SourceColorSRGB, FormatR16G16B16A16_UNORM, []color.NRGBA64{{R: 12345, G: 23456, B: 34567, A: 45678}, {R: 65535, G: 65535, B: 65535, A: 1}, {}}, false, 4},
		{"bc7-srgb", false, SourceColorSRGB, FormatBC7_UNORM_SRGB, partial[:1], false, 4 * 257},
		{"transparent-mip", false, SourceColorSRGB, FormatR8G8B8A8_UNORM_SRGB, []color.NRGBA64{{R: 65535, G: 65535, B: 65535, A: 65535}, {R: 65535}}, true, 257},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			bounds := image.Rect(0, 0, len(test.pixels), 1)

			var (
				straight   image.Image
				associated image.Image
			)

			if test.wide {
				input := image.NewNRGBA64(bounds)
				premultiplied := image.NewRGBA64(bounds)

				for column, pixel := range test.pixels {
					input.SetNRGBA64(column, 0, pixel)
					premultiplied.Set(column, 0, pixel)
				}

				straight = input
				associated = premultiplied
			} else {
				input := image.NewNRGBA(bounds)
				premultiplied := image.NewRGBA(bounds)

				for column, pixel := range test.pixels {
					input.Set(column, 0, pixel)
					premultiplied.Set(column, 0, pixel)
				}

				straight = input
				associated = premultiplied
			}

			options := Options{Format: test.format, SourceColorSpace: test.space}
			selection := Selection{}

			if test.mips {
				options.Mipmaps = &MipmapOptions{Filter: FilterBox}
				selection.Mip = 1
			}

			inputs := []image.Image{straight, associated}

			for _, input := range inputs {
				texture := encodedImage(t, input, &options)
				actual, err := texture.Image(&DecodeOptions{Surface: selection})
				check(t, err)

				if texture.Metadata().Format != test.format || texture.Metadata().Width != bounds.Dx() {
					t.Fatal("lost format or dimensions")
				}

				for column := range actual.Bounds().Dx() {
					pixel := test.pixels[column]

					if test.mips {
						// Opaque white plus transparent red has white straight RGB,
						// with half coverage, independent of the hidden red sample.
						pixel = color.NRGBA64{R: 65535, G: 65535, B: 65535, A: 32768}
					}

					red, green, blue, alpha := actual.At(column, 0).RGBA()
					if red > alpha || green > alpha || blue > alpha {
						t.Fatalf("invalid Go premultiplication: %d %d %d / %d", red, green, blue, alpha)
					}

					channels := [...]uint32{red, green, blue, alpha}
					reference := [...]uint16{pixel.R, pixel.G, pixel.B, pixel.A}

					for channel, value := range channels {
						want := float64(reference[channel])

						if channel < 3 {
							want /= 65535

							if test.space == SourceColorSRGB {
								want = linearSample(reference[channel])
							}

							if test.format.Info().SRGB {
								if want <= 0.0031308 {
									want *= 12.92
								} else {
									want = 1.055*math.Pow(want, 1/2.4) - 0.055
								}
							}

							want *= float64(pixel.A)
						}

						if math.Abs(float64(value)-want) > float64(test.tolerance) {
							t.Fatalf("%T pixel %d channel %d: %d, want %g", input, column, channel, value, want)
						}
					}
				}
			}
		})
	}
}

func testNativeAssociation(t *testing.T) {
	pattern := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	pattern.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	pattern.SetNRGBA(1, 0, color.NRGBA{R: 255})
	texture := encodedImage(t, pattern, &Options{Format: FormatR8G8B8A8_UNORM_SRGB})
	mips, err := texture.GenerateMipmaps(&MipmapOptions{Filter: FilterBox})
	check(t, err)
	mips = savedTexture(t, mips)
	actual, err := mips.Image(&DecodeOptions{Surface: Selection{Mip: 1}})
	check(t, err)

	if actual.(*image.NRGBA).NRGBAAt(0, 0) != (color.NRGBA{R: 255, G: 255, B: 255, A: 128}) {
		t.Fatalf("straight alpha mip: %v", actual.At(0, 0))
	}

	conventions := []bool{false, true}

	for _, encoded := range conventions {
		premultiplied, alphaErr := mips.WithAlphaMode(AlphaPremultiplied, encoded)
		check(t, alphaErr)
		premultiplied = savedTexture(t, premultiplied)
		settings := DecodeOptions{Surface: Selection{Mip: 1}}

		if encoded {
			settings.Premultiplication = SRGBPremultipliedEncoded
		}

		actual, err = premultiplied.Image(&settings)
		check(t, err)

		if actual.(*image.RGBA).RGBAAt(0, 0) != (color.RGBA{R: 128, G: 128, B: 128, A: 128}) {
			t.Fatalf("encoded=%v: %v", encoded, actual.At(0, 0))
		}

		settings.Precision = Precision16
		actual, err = premultiplied.Image(&settings)
		check(t, err)

		if actual.(*image.RGBA64).RGBA64At(0, 0) != (color.RGBA64{R: 32896, G: 32896, B: 32896, A: 32896}) {
			t.Fatalf("wide quantized white: %v", actual.At(0, 0))
		}

		straight, alphaErr := premultiplied.WithAlphaMode(AlphaStraight, encoded)
		check(t, alphaErr)
		actual, err = savedTexture(t, straight).Image(&DecodeOptions{Surface: Selection{Mip: 1}})
		check(t, err)

		if actual.(*image.NRGBA).NRGBAAt(0, 0).R != 255 {
			t.Fatal("alpha reversal changed white")
		}
	}

	premultiplied := image.NewRGBA(image.Rect(0, 0, 1, 1))
	premultiplied.SetRGBA(0, 0, color.RGBA{R: 64, G: 64, B: 64, A: 128})

	withoutAlpha := encodedImage(t, premultiplied, &Options{Format: FormatR8_UNORM})
	if withoutAlpha.pixels[0] != 128 || withoutAlpha.Metadata().AlphaMode() != AlphaOpaque {
		t.Fatalf("alpha discarded without unassociation: %v", withoutAlpha.pixels)
	}

	premultiplied.SetRGBA(0, 0, color.RGBA{R: 200, A: 128})
	invalid, err := FromImage(premultiplied)
	check(t, err)

	_, err = invalid.Image(nil)
	if err == nil {
		t.Fatal("returned invalid Go premultiplication")
	}

	clamped, err := invalid.Image(&DecodeOptions{Display: DisplayClamp})
	check(t, err)

	if clamped.(*image.RGBA).RGBAAt(0, 0) != (color.RGBA{R: 128, A: 128}) {
		t.Fatal("explicit premultiplication clamp failed")
	}

	premultiplied.SetRGBA(0, 0, color.RGBA{R: 200})
	invalid, err = FromImage(premultiplied)
	check(t, err)
	invalid, err = invalid.Convert(FormatR8G8B8A8_UNORM_SRGB, nil)
	check(t, err)
	invalid = savedTexture(t, invalid)

	_, err = invalid.Image(nil)
	if err == nil {
		t.Fatal("zero alpha concealed invalid sRGB premultiplication")
	}

	clamped, err = invalid.Image(&DecodeOptions{Display: DisplayClamp})
	check(t, err)

	if clamped.(*image.RGBA).RGBAAt(0, 0) != (color.RGBA{}) {
		t.Fatal("explicit zero-alpha clamp failed")
	}
}

func testCompleteColor(t *testing.T) {
	texture := fixture(t, "cube-array")
	fixtureTexture := texture
	original := bytes.Clone(texture.pixels)
	surfaces := make([]Subresource, len(texture.surfaces))

	for index, surface := range texture.surfaces {
		surface.Pixels = bytes.Clone(texture.pixels[surface.offset : surface.offset+surface.SlicePitch])

		for offset := 3; offset < len(surface.Pixels); offset += 4 {
			surface.Pixels[offset] = 128
		}

		surfaces[index] = surface
	}

	texture, err := NewTexture(texture.Metadata(), surfaces)
	check(t, err)
	linear, err := texture.Convert(FormatR32G32B32A32_FLOAT, nil)
	check(t, err)
	linear, err = linear.WithAlphaMode(AlphaPremultiplied, false)
	check(t, err)
	encoded, err := linear.Convert(FormatR8G8B8A8_UNORM_SRGB, nil)
	check(t, err)

	encoded = savedTexture(t, encoded)
	if encoded.Metadata().ArraySize != 12 || encoded.Metadata().MipLevels != 3 || !encoded.Metadata().IsCube() || encoded.Metadata().AlphaMode() != AlphaPremultiplied {
		t.Fatalf("lost complete texture metadata: %+v", encoded.Metadata())
	}

	for item := range 12 {
		for mip := range 3 {
			actual, imageErr := encoded.Image(&DecodeOptions{Surface: Selection{Item: item, Mip: mip}})
			check(t, imageErr)

			pixel := actual.(*image.RGBA).RGBAAt(0, 0)
			if pixel.R > pixel.A || pixel.G > pixel.A || pixel.B > pixel.A || pixel.A != 128 {
				t.Fatalf("invalid complete-texture pixel: %v", pixel)
			}

			// Native linear samples are encoded after association. Compare their
			// decoded linear energy, not a matching color-conversion round trip.
			channels := [...]uint8{pixel.R, pixel.G}
			expected := [...]float64{float64(item*16) / 255, float64(mip*64) / 255}

			for channel, value := range channels {
				straight := uint16(float64(value) / 128 * 65535)
				if math.Abs(linearSample(straight)-expected[channel]) > 0.02 {
					t.Fatalf("item %d mip %d channel %d: %v", item, mip, channel, pixel)
				}
			}
		}
	}

	if !bytes.Equal(fixtureTexture.pixels, original) || surfaces[0].Pixels[3] != 128 {
		t.Fatal("modified caller-owned storage")
	}

	volume := fixture(t, "volume")
	generated, err := volume.GenerateMipmaps(nil)
	check(t, err)

	generated = savedTexture(t, generated)
	if generated.Metadata() != volume.Metadata() {
		t.Fatalf("volume mips lost metadata: %+v", generated.Metadata())
	}

	// Four equally weighted depth slices have blue 0, 64, 128, 192.
	last, err := generated.Image(&DecodeOptions{Surface: Selection{Mip: 2}})
	check(t, err)

	if last.(*image.NRGBA).NRGBAAt(0, 0).B != 96 {
		t.Fatalf("volume depth filtering: %v", last.At(0, 0))
	}
}

func TestXRRange(t *testing.T) {
	pixels := make([]byte, 4)
	binary.LittleEndian.PutUint32(pixels, 1023<<10|384<<20|3<<30)
	metadata := Metadata{Width: 1, Height: 1, Depth: 1, ArraySize: 1, MipLevels: 1, Format: FormatR10G10B10_XR_BIAS_A2_UNORM, Dimension: Dimension2D}
	texture, err := NewTexture(metadata, []Subresource{{Width: 1, Height: 1, RowPitch: 4, SlicePitch: 4, Pixels: pixels}})
	check(t, err)
	texture = savedTexture(t, texture)

	_, err = texture.Image(nil)
	if err == nil {
		t.Fatal("strict Image accepted XR")
	}

	var encoded bytes.Buffer

	check(t, texture.Save(&encoded, nil))

	_, err = Decode(bytes.NewReader(encoded.Bytes()))
	if err == nil {
		t.Fatal("strict Decode accepted XR")
	}

	_, err = DecodeConfig(bytes.NewReader(encoded.Bytes()))
	if err == nil {
		t.Fatal("DecodeConfig accepted XR")
	}

	clamped, err := DecodeWithOptions(&encoded, &DecodeOptions{Display: DisplayClamp})
	check(t, err)

	pixel := clamped.(*image.NRGBA64).NRGBA64At(0, 0)
	if pixel != (color.NRGBA64{G: 65535, A: 65535}) {
		t.Fatalf("XR clamp: %+v", pixel)
	}

	native, err := texture.Convert(FormatR32G32B32A32_FLOAT, nil)
	check(t, err)

	red := math.Float32frombits(binary.LittleEndian.Uint32(native.pixels))
	green := math.Float32frombits(binary.LittleEndian.Uint32(native.pixels[4:]))

	if math.Abs(float64(red)+384.0/510) > 0.00001 || math.Abs(float64(green)-639.0/510) > 0.00001 {
		t.Fatalf("native XR range lost: %g, %g", red, green)
	}
}

func TestOneLevelMipmaps(t *testing.T) {
	base, err := FromImage(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	check(t, err)
	options := []*MipmapOptions{nil, {Levels: 1}}

	for _, settings := range options {
		result, mipErr := base.GenerateMipmaps(settings)
		check(t, mipErr)

		if result != base {
			t.Fatal("one-level no-op did not return original texture")
		}
	}

	legacy, err := os.ReadFile("testdata/legacy-rgb24.dds")
	check(t, err)
	binary.LittleEndian.PutUint32(legacy[8:], binary.LittleEndian.Uint32(legacy[8:])|0x20000)
	binary.LittleEndian.PutUint32(legacy[28:], 2)
	binary.LittleEndian.PutUint32(legacy[108:], binary.LittleEndian.Uint32(legacy[108:])|0x400008)
	legacy = append(legacy, 12, 34, 56)
	normalized, err := Load(bytes.NewReader(legacy), nil)
	check(t, err)

	if len(normalized.original) == 0 {
		t.Fatal("test requires retained legacy representation")
	}

	textures := []*Texture{fixture(t, "array"), fixture(t, "cube-array"), fixture(t, "volume"), fixture(t, "one-dimensional"), fixture(t, "typeless"), fixture(t, "bc7"), normalized}

	for _, texture := range textures {
		result, mipErr := texture.GenerateMipmaps(&MipmapOptions{Levels: 1})
		check(t, mipErr)

		if texture.Metadata().MipLevels == 1 && result != texture {
			t.Fatal("one-level no-op did not return original texture")
		}

		want := texture.Metadata()
		want.MipLevels = 1

		if result.Metadata() != want || result.SubresourceCount() != want.ArraySize*want.Depth || len(result.original) != 0 {
			t.Fatalf("base-only metadata: %+v", result.Metadata())
		}

		result = savedTexture(t, result)
		if result.Metadata() != want {
			t.Fatal("save restored removed mips or changed metadata")
		}

		for item := range want.ArraySize {
			for slice := range want.Depth {
				selection := Selection{Item: item, Slice: slice}
				before, surfaceErr := texture.Surface(selection)
				check(t, surfaceErr)
				after, surfaceErr := result.Surface(selection)
				check(t, surfaceErr)

				if before.Width != after.Width || before.Height != after.Height || !bytes.Equal(before.Pixels, after.Pixels) {
					t.Fatalf("base surface changed: %+v", selection)
				}
			}
		}
	}
}

func TestBCEncoding(t *testing.T) {
	cases := []encodingCase{
		{"bc1", FormatBC1_UNORM, 4, 0.08, false},
		{"bc2", FormatBC2_UNORM, 4, 0.08, false},
		{"bc3", FormatBC3_UNORM, 4, 0.08, false},
		{"bc4", FormatBC4_UNORM, 1, 0.04, false},
		{"bc5", FormatBC5_UNORM, 2, 0.04, false},
		// SNORM spans one unit per block: half a 1/7 palette step plus 8-bit quantization.
		{"bc4-snorm", FormatBC4_SNORM, 1, 0.08, false},
		{"bc5-snorm", FormatBC5_SNORM, 2, 0.08, false},
		{"bc6-unsigned", FormatBC6H_UF16, 3, 0.15, false},
		{"bc6-signed", FormatBC6H_SF16, 3, 0.15, false},
		{"bc7", FormatBC7_UNORM, 4, 0.08, false},
		{"bc7-srgb-mips", FormatBC7_UNORM_SRGB, 4, 0.08, true},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			pattern := image.NewNRGBA(image.Rect(0, 0, 5, 3))
			values := make([]float32, 5*3*4)
			kind := test.format.Info().DataType

			for row := range 3 {
				for column := range 5 {
					step := uint8(16*column + 8*row)
					pixel := color.NRGBA{R: 32 + step, G: 64 + step, B: 96 + step, A: 85 + uint8(17*(column+row))}

					if test.format == FormatBC1_UNORM {
						pixel.A = 255

						if column == 0 && row == 0 {
							pixel.A = 0
						}
					}

					pattern.SetNRGBA(column, row, pixel)
					offset := (row*5 + column) * 4
					channels := [...]uint8{pixel.R, pixel.G, pixel.B, pixel.A}

					for channel, value := range channels {
						values[offset+channel] = float32(value) / 255
					}

					switch kind {
					case TypeSNorm:
						values[offset] = -0.75 + float32(column)*0.25 + float32(row)*0.125
						values[offset+1] = -values[offset]
					case TypeFloat:
						variation := float32(column)*0.125 + float32(row)*0.0625
						values[offset] = 2 + variation
						values[offset+1] = 1 + variation
						values[offset+2] = 3 + variation

						if test.format == FormatBC6H_SF16 {
							values[offset] *= -1
							values[offset+2] *= -1
						}
					}
				}
			}

			var compressed *Texture

			if kind == TypeUNorm {
				options := Options{Format: test.format}

				if test.mips {
					options.Mipmaps = &MipmapOptions{}
				}

				compressed = encodedImage(t, pattern, &options)
			} else {
				pixels := make([]byte, len(values)*4)

				for index, value := range values {
					binary.LittleEndian.PutUint32(pixels[index*4:], math.Float32bits(value))
				}

				metadata := Metadata{Width: 5, Height: 3, Depth: 1, ArraySize: 1, MipLevels: 1, Format: FormatR32G32B32A32_FLOAT, Dimension: Dimension2D}
				native, err := NewTexture(metadata, []Subresource{{Width: 5, Height: 3, RowPitch: 80, SlicePitch: 240, Pixels: pixels}})
				check(t, err)
				compressed, err = native.Compress(test.format, nil)
				check(t, err)
			}

			compressed = savedTexture(t, compressed)
			levels := 1

			if test.mips {
				levels = 3
			}

			metadata := compressed.Metadata()
			if metadata.Format != test.format || metadata.Width != 5 || metadata.Height != 3 || metadata.MipLevels != levels || compressed.SubresourceCount() != levels {
				t.Fatalf("encoding metadata: %+v", metadata)
			}

			decoded, err := compressed.Decompress(FormatUNKNOWN)
			check(t, err)
			filter := FilterFlags(0)

			if metadata.IsSRGB() {
				filter = FilterSRGBOut
			}

			decoded, err = decoded.Convert(FormatR32G32B32A32_FLOAT, &ConversionOptions{Filter: filter})
			check(t, err)

			for index, want := range values {
				channel := index % 4
				if channel >= test.channels || (test.format == FormatBC1_UNORM && index < 3) {
					continue
				}

				actual := math.Float32frombits(binary.LittleEndian.Uint32(decoded.pixels[index*4:]))
				if math.IsNaN(float64(actual)) || math.Abs(float64(actual-want)) > test.tolerance {
					t.Fatalf("pixel %d channel %d: %g, want %g (tolerance %g)", index/4, channel, actual, want, test.tolerance)
				}
			}
		})
	}
}

func encodedImage(t *testing.T, source image.Image, options *Options) *Texture {
	t.Helper()

	var encoded bytes.Buffer

	check(t, Encode(&encoded, source, options))
	texture, err := Load(&encoded, nil)
	check(t, err)

	return texture
}

func savedTexture(t *testing.T, texture *Texture) *Texture {
	t.Helper()

	var encoded bytes.Buffer

	check(t, texture.Save(&encoded, nil))
	result, err := Load(&encoded, nil)
	check(t, err)

	return result
}

func linearSample(value uint16) float64 {
	sample := float64(value) / 65535
	if sample <= 0.04045 {
		return sample / 12.92
	}

	return math.Pow((sample+0.055)/1.055, 2.4)
}

func fixture(t *testing.T, name string) *Texture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".dds"))
	check(t, err)
	texture, err := Load(bytes.NewReader(data), nil)
	check(t, err)

	return texture
}

func check(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

func compareImages(t *testing.T, actual image.Image, expected image.Image, tolerance uint32) {
	t.Helper()

	if actual.Bounds().Size() != expected.Bounds().Size() {
		t.Fatal("image dimensions differ")
	}

	for row := range actual.Bounds().Dy() {
		for column := range actual.Bounds().Dx() {
			actualRed, actualGreen, actualBlue, actualAlpha := actual.At(column+actual.Bounds().Min.X, row+actual.Bounds().Min.Y).RGBA()
			expectedRed, expectedGreen, expectedBlue, expectedAlpha := expected.At(column+expected.Bounds().Min.X, row+expected.Bounds().Min.Y).RGBA()
			actualValues := [...]uint32{actualRed, actualGreen, actualBlue, actualAlpha}
			expectedValues := [...]uint32{expectedRed, expectedGreen, expectedBlue, expectedAlpha}

			for channel, actualValue := range actualValues {
				expectedValue := expectedValues[channel]
				if max(actualValue, expectedValue)-min(actualValue, expectedValue) > tolerance {
					t.Fatalf("%T pixel (%d,%d) channel %d: got %d, want %d", actual, column, row, channel, actualValue, expectedValue)
				}
			}
		}
	}
}
