// Command fixtures regenerates the small fixture corpus. It is never run by tests.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/coalaura/dds"
)

type fixture struct {
	name   string
	format dds.Format
}

var formats = []fixture{
	{"bc1", dds.FormatBC1_UNORM},
	{"bc2", dds.FormatBC2_UNORM},
	{"bc3", dds.FormatBC3_UNORM},
	{"bc4", dds.FormatBC4_UNORM},
	{"bc4s", dds.FormatBC4_SNORM},
	{"bc5", dds.FormatBC5_UNORM},
	{"bc5s", dds.FormatBC5_SNORM},
	{"bc6u", dds.FormatBC6H_UF16},
	{"bc6s", dds.FormatBC6H_SF16},
	{"bc7", dds.FormatBC7_UNORM},
	{"bc7-srgb", dds.FormatBC7_UNORM_SRGB},
	{"rgba16", dds.FormatR16G16B16A16_UNORM},
	{"float32", dds.FormatR32G32B32A32_FLOAT},
	{"rgb565", dds.FormatB5G6R5_UNORM},
	{"rgb10a2", dds.FormatR10G10B10A2_UNORM},
	{"r16uint", dds.FormatR16_UINT},
}

func main() {
	err := generate()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
	for _, entry := range formats {
		values := [4]float32{0.25, 0.5, 0.75, 1}

		if entry.name == "bc4s" || entry.name == "bc5s" {
			values[0] = -0.5
		}

		if entry.name == "bc6u" || entry.name == "bc6s" || entry.name == "float32" {
			values = [4]float32{2, 4, 8, 1}

			if entry.name != "bc6u" {
				values[0] = -2
			}
		}

		texture, err := constantTexture(5, 3, values)
		if err != nil {
			return err
		}

		if entry.format.Info().Compressed {
			texture, err = texture.Compress(entry.format, nil)
		} else {
			texture, err = texture.Convert(entry.format, nil)
		}

		if err != nil {
			return fmt.Errorf("%s: %w", entry.name, err)
		}

		err = save(entry.name, texture)
		if err != nil {
			return err
		}
	}

	// These legacy files are constructed from the DDS/block specifications, without
	// using DirectXTex or this package's writer. All selectors choose endpoint zero.
	red := []byte{0x00, 0xf8, 0, 0, 0, 0, 0, 0}
	alpha4 := bytes.Repeat([]byte{0x88}, 8)
	alpha8 := []byte{128, 0, 0, 0, 0, 0, 0, 0}
	blocks := map[string][]byte{
		"DXT1": red,
		"DXT2": append(append([]byte(nil), alpha4...), []byte{0, 0x80, 0, 0, 0, 0, 0, 0}...),
		"DXT3": append(append([]byte(nil), alpha4...), red...),
		"DXT4": append(append([]byte(nil), alpha8...), []byte{0, 0x80, 0, 0, 0, 0, 0, 0}...),
		"DXT5": append(append([]byte(nil), alpha8...), red...),
		"ATI1": {64, 0, 0, 0, 0, 0, 0, 0},
		"ATI2": {64, 0, 0, 0, 0, 0, 0, 0, 128, 0, 0, 0, 0, 0, 0, 0},
	}

	for fourCC, block := range blocks {
		header := legacyHeader(4, 4)
		binary.LittleEndian.PutUint32(header[80:], 4)
		copy(header[84:], fourCC)

		err := write("legacy-"+fourCC+".dds", append(header, block...))
		if err != nil {
			return err
		}
	}

	header := legacyHeader(3, 2)
	binary.LittleEndian.PutUint32(header[80:], 0x40)
	binary.LittleEndian.PutUint32(header[88:], 24)
	binary.LittleEndian.PutUint32(header[92:], 0xff0000)
	binary.LittleEndian.PutUint32(header[96:], 0xff00)
	binary.LittleEndian.PutUint32(header[100:], 0xff)

	err := write("legacy-rgb24.dds", append(header, bytes.Repeat([]byte{32, 64, 128}, 6)...))
	if err != nil {
		return err
	}

	header = legacyHeader(1, 1)
	binary.LittleEndian.PutUint32(header[80:], 4)
	copy(header[84:], "DXT1")

	err = write("legacy-bc1-alpha.dds", append(header, []byte{0, 0, 255, 255, 255, 255, 255, 255}...))
	if err != nil {
		return err
	}

	err = structures()
	if err != nil {
		return err
	}
	// FFmpeg is a maintainer-only independent decoder, never a runtime dependency.
	references := []string{"legacy-DXT1", "legacy-DXT3", "legacy-DXT5", "legacy-rgb24"}

	for _, name := range references {
		command := exec.Command("ffmpeg", "-v", "error", "-y", "-i", filepath.Join("testdata", name+".dds"), "-frames:v", "1", filepath.Join("testdata", name+".png"))

		output, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("reference %s: %w: %s", name, err, output)
		}
	}

	return nil
}

func structures() error {
	metadata := dds.Metadata{Width: 4, Height: 4, Depth: 1, ArraySize: 12, MipLevels: 3, Format: dds.FormatR8G8B8A8_UNORM, Dimension: dds.Dimension2D, MiscFlags: 4, MiscFlags2: uint32(dds.AlphaStraight)}
	names := []string{"cube-array", "array", "volume", "one-dimensional", "typeless"}

	for _, name := range names {
		current := metadata

		switch name {
		case "array":
			current.MiscFlags = 0
			current.ArraySize = 2
		case "volume":
			current.MiscFlags = 0
			current.ArraySize = 1
			current.Depth = 4
			current.Dimension = dds.Dimension3D
		case "one-dimensional":
			current.MiscFlags = 0
			current.ArraySize = 2
			current.Height = 1
			current.Dimension = dds.Dimension1D
		case "typeless":
			current.MiscFlags = 0
			current.ArraySize = 1
			current.Format = dds.FormatR8G8B8A8_TYPELESS
		}

		surfaces := make([]dds.Subresource, 0, 36)

		for item := 0; item < current.ArraySize; item++ {
			for mip := 0; mip < current.MipLevels; mip++ {
				width := max(1, current.Width>>mip)
				height := max(1, current.Height>>mip)

				for slice := 0; slice < max(1, current.Depth>>mip); slice++ {
					pixels := bytes.Repeat([]byte{byte(item * 16), byte(mip * 64), byte(slice * 64), 255}, width*height)
					surfaces = append(surfaces, dds.Subresource{Width: width, Height: height, RowPitch: width * 4, SlicePitch: len(pixels), Pixels: pixels})
				}
			}
		}

		texture, err := dds.NewTexture(current, surfaces)
		if err != nil {
			return err
		}

		err = save(name, texture)
		if err != nil {
			return err
		}
	}

	return nil
}

func constantTexture(width, height int, values [4]float32) (*dds.Texture, error) {
	pixels := make([]byte, width*height*16)

	for offset := 0; offset < len(pixels); offset += 16 {
		for channel, value := range values {
			binary.LittleEndian.PutUint32(pixels[offset+channel*4:], math.Float32bits(value))
		}
	}

	metadata := dds.Metadata{Width: width, Height: height, Depth: 1, ArraySize: 1, MipLevels: 1, Format: dds.FormatR32G32B32A32_FLOAT, Dimension: dds.Dimension2D, MiscFlags2: uint32(dds.AlphaStraight)}
	surfaces := []dds.Subresource{{Width: width, Height: height, RowPitch: width * 16, SlicePitch: len(pixels), Pixels: pixels}}
	return dds.NewTexture(metadata, surfaces)
}

func legacyHeader(width, height uint32) []byte {
	header := make([]byte, 128)
	copy(header, "DDS ")
	binary.LittleEndian.PutUint32(header[4:], 124)
	binary.LittleEndian.PutUint32(header[8:], 0x1007)
	binary.LittleEndian.PutUint32(header[12:], height)
	binary.LittleEndian.PutUint32(header[16:], width)
	binary.LittleEndian.PutUint32(header[76:], 32)
	binary.LittleEndian.PutUint32(header[108:], 0x1000)

	return header
}

func save(name string, texture *dds.Texture) error {
	var output bytes.Buffer

	err := texture.Save(&output, nil)
	if err != nil {
		return err
	}

	return write(name+".dds", output.Bytes())
}

func write(name string, data []byte) error {
	return os.WriteFile(filepath.Join("testdata", name), data, 0644)
}
