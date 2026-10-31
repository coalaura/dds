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
		check(t, err)

		if _, ok := decoded.(*image.RGBA); !ok {
			t.Fatalf("premultiplied image type: %T", decoded)
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
