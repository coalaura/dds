package dds

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

// Inspect reads only the DDS header (128 or 148 bytes), without pixel decoding.
// Legacy formats are described in their normalized DirectXTex representation.
func Inspect(reader io.Reader, options *LoadOptions) (Metadata, error) {
	flags, limit, err := loadSettings(options)
	if err != nil {
		return Metadata{}, err
	}

	header, err := readHeader(reader)
	if err != nil {
		return Metadata{}, err
	}

	metadata, err := nativeInspect(header, flags)
	if err != nil {
		return Metadata{}, err
	}

	_, _, err = layout(metadata, limit)
	return metadata, err
}

// InspectAt inspects a header without changing a file's seek position.
func InspectAt(reader io.ReaderAt, options *LoadOptions) (Metadata, error) {
	if reader == nil {
		return Metadata{}, fmt.Errorf("dds: nil reader")
	}

	return Inspect(io.NewSectionReader(reader, 0, 148), options)
}

// Load reads a complete texture, retaining native compressed/uncompressed data.
// Legacy masks without a DXGI equivalent are normalized by DirectXTex.
func Load(reader io.Reader, options *LoadOptions) (*Texture, error) {
	return load(reader, options, -1)
}

// LoadAt reads exactly size bytes from a random-access source without altering
// its seek position. InspectAt can inspect metadata without reading the payload.
func LoadAt(reader io.ReaderAt, size int64, options *LoadOptions) (*Texture, error) {
	_, limit, err := loadSettings(options)
	if err != nil {
		return nil, err
	}

	if reader == nil || size < 128 || size > limit {
		return nil, fmt.Errorf("dds: nil reader or invalid input size %d", size)
	}

	return load(io.NewSectionReader(reader, 0, size), options, size)
}

func load(reader io.Reader, options *LoadOptions, size int64) (*Texture, error) {
	flags, limit, err := loadSettings(options)
	if err != nil {
		return nil, err
	}

	header, err := readHeader(reader)
	if err != nil {
		return nil, err
	}

	metadata, err := nativeInspect(header, flags)
	if err != nil {
		return nil, err
	}

	_, _, err = layout(metadata, limit)
	if err != nil {
		return nil, err
	}
	// Append directly to the header rather than concatenating two whole-file buffers.
	data := header

	if size >= int64(len(header)) {
		data = make([]byte, len(header), int(size))
		copy(data, header)
	}

	buffer := make([]byte, 32<<10)
	limited := io.LimitReader(reader, limit-int64(len(header))+1)

	for {
		count, readErr := limited.Read(buffer)

		data = append(data, buffer[:count]...)
		if int64(len(data)) > limit {
			return nil, fmt.Errorf("dds: input exceeds MaxBytes")
		}

		if errors.Is(readErr, io.EOF) {
			break
		}

		if readErr != nil {
			return nil, readErr
		}

		if count == 0 {
			return nil, io.ErrNoProgress
		}
	}

	if size >= 0 && int64(len(data)) != size {
		return nil, io.ErrUnexpectedEOF
	}

	texture, err := nativeLoad(data, flags)
	if err != nil {
		return nil, err
	}

	if flags == 0 && len(header) == 128 && !bytes.Equal(data[128:], texture.pixels) {
		texture.original = data
	}

	return texture, nil
}

func readHeader(reader io.Reader) ([]byte, error) {
	if reader == nil {
		return nil, fmt.Errorf("dds: nil reader")
	}

	header := make([]byte, 148)

	_, err := io.ReadFull(reader, header[:128])
	if err != nil {
		return nil, err
	}

	if string(header[:4]) != "DDS " {
		return nil, fmt.Errorf("dds: invalid signature")
	}

	if string(header[84:88]) == "RXGB" {
		return nil, fmt.Errorf("dds: game-specific RXGB swizzle is unsupported")
	}

	if string(header[84:88]) != "DX10" {
		return header[:128], nil
	}

	_, err = io.ReadFull(reader, header[128:])
	return header, err
}
