package dds

/*
#cgo CXXFLAGS: -std=c++17 -O2 -DNDEBUG -DDDS_NO_WIC -I${SRCDIR}/internal/native/DirectXTex/DirectXTex -I${SRCDIR}/internal/native/DirectXMath/Inc -I${SRCDIR}/internal/native/DirectX-Headers/include
#cgo !windows CXXFLAGS: -I${SRCDIR}/internal/native/DirectX-Headers/include/wsl/stubs -I${SRCDIR}/internal/native/sal
#cgo windows LDFLAGS: -static-libstdc++ -static-libgcc
#cgo linux LDFLAGS: -static-libstdc++ -static-libgcc -lm
#cgo darwin LDFLAGS: -lc++
#include "bridge.h"
*/
import "C"

import (
	"fmt"
	"io"
	"unsafe"
)

func (format Format) Info() FormatInfo {
	var bits, precision, kind C.uint32_t
	flags := uint32(C.dds_format_info(C.uint32_t(format), &bits, &precision, &kind))
	return FormatInfo{
		Valid:        flags&1 != 0,
		Compressed:   flags&2 != 0,
		Typeless:     flags&4 != 0,
		SRGB:         flags&8 != 0,
		Alpha:        flags&16 != 0,
		Planar:       flags&32 != 0,
		Video:        flags&64 != 0,
		BitsPerPixel: int(bits),
		BitsPerColor: int(precision),
		DataType:     DataType(kind),
	}
}

// Pitch returns DirectXTex's tightly packed row and slice pitches.
func (format Format) Pitch(width, height int) (int, int, error) {
	if width <= 0 || height <= 0 || width > maxDimension || height > maxDimension {
		return 0, 0, fmt.Errorf("dds: invalid dimensions %dx%d", width, height)
	}
	var row, slice C.size_t
	status := C.dds_pitch(C.uint32_t(format), C.size_t(width), C.size_t(height), &row, &slice)
	return int(row), int(slice), nativeError("compute pitch", status)
}

func nativeInspect(data []byte, flags LoadFlags) (Metadata, error) {
	var metadata C.dds_metadata
	status := C.dds_inspect(bytePointer(data), C.size_t(len(data)), C.uint32_t(flags), &metadata)
	return goMetadata(metadata), nativeError("inspect header", status)
}

func nativeLoad(data []byte, flags LoadFlags) (*Texture, error) {
	var result *C.dds_result
	status := C.dds_load(bytePointer(data), C.size_t(len(data)), C.uint32_t(flags), &result)
	err := nativeError("load texture", status)
	if err != nil {
		return nil, err
	}
	defer C.dds_release(result)
	return copyResult(result), nil
}

func nativeProcess(texture *Texture, operation, target, flags uint32, threshold float32, levels int, writer io.Writer) (*Texture, error) {
	metadata := cMetadata(texture.metadata)
	surfaces := make([]C.dds_surface, len(texture.surfaces))
	for index, surface := range texture.surfaces {
		surfaces[index] = C.dds_surface{
			width:  C.size_t(surface.Width),
			height: C.size_t(surface.Height),
			row:    C.size_t(surface.RowPitch),
			slice:  C.size_t(surface.SlicePitch),
			offset: C.size_t(surface.offset),
		}
	}

	var result *C.dds_result
	status := C.dds_process(&metadata, &surfaces[0], C.size_t(len(surfaces)), bytePointer(texture.pixels), C.uint32_t(operation), C.uint32_t(target), C.uint32_t(flags), C.float(threshold), C.size_t(levels), &result)
	err := nativeError(operationName(operation), status)
	if err != nil {
		return nil, err
	}
	defer C.dds_release(result)
	if writer != nil {
		var count, size C.size_t
		var pixels *C.uint8_t
		C.dds_result_info(result, &metadata, &count, &pixels, &size)
		data := unsafe.Slice((*byte)(unsafe.Pointer(pixels)), int(size))
		written, writeErr := writer.Write(data)
		if writeErr == nil && written != len(data) {
			writeErr = io.ErrShortWrite
		}
		return nil, writeErr
	}
	return copyResult(result), nil
}

func copyResult(result *C.dds_result) *Texture {
	var metadata C.dds_metadata
	var count, size C.size_t
	var pixels *C.uint8_t
	C.dds_result_info(result, &metadata, &count, &pixels, &size)
	surfaces := make([]C.dds_surface, int(count))
	C.dds_result_surfaces(result, &surfaces[0])
	texture := &Texture{
		metadata: goMetadata(metadata),
		surfaces: make([]Subresource, int(count)),
		pixels:   append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(pixels)), int(size))...),
	}
	for index, surface := range surfaces {
		texture.surfaces[index] = Subresource{
			Width:      int(surface.width),
			Height:     int(surface.height),
			RowPitch:   int(surface.row),
			SlicePitch: int(surface.slice),
			offset:     int(surface.offset),
		}
	}
	return texture
}

func cMetadata(metadata Metadata) C.dds_metadata {
	return C.dds_metadata{
		width:     C.size_t(metadata.Width),
		height:    C.size_t(metadata.Height),
		depth:     C.size_t(metadata.Depth),
		arrays:    C.size_t(metadata.ArraySize),
		mips:      C.size_t(metadata.MipLevels),
		format:    C.uint32_t(metadata.Format),
		dimension: C.uint32_t(metadata.Dimension),
		misc:      C.uint32_t(metadata.MiscFlags),
		misc2:     C.uint32_t(metadata.MiscFlags2),
	}
}

func goMetadata(metadata C.dds_metadata) Metadata {
	return Metadata{
		Width:      int(metadata.width),
		Height:     int(metadata.height),
		Depth:      int(metadata.depth),
		ArraySize:  int(metadata.arrays),
		MipLevels:  int(metadata.mips),
		Format:     Format(metadata.format),
		Dimension:  Dimension(metadata.dimension),
		MiscFlags:  uint32(metadata.misc),
		MiscFlags2: uint32(metadata.misc2),
	}
}

func nativeError(operation string, status C.uint32_t) error {
	if status == 0 {
		return nil
	}

	reason := "native operation failed"

	switch uint32(status) {
	case 0x80070057:
		reason = "invalid or incompatible parameters"
	case 0x80070032, 0x80004001:
		reason = "format or operation is unsupported"
	case 0x8007000d:
		reason = "invalid DDS data"
	case 0x80070026:
		reason = "truncated pixel payload"
	case 0x8007000e:
		reason = "native allocation failed"
	case 0x80070216:
		reason = "size arithmetic overflow"
	}

	return fmt.Errorf("dds: %s: %s (DirectXTex HRESULT 0x%08x)", operation, reason, uint32(status))
}

func bytePointer(data []byte) *C.uint8_t {
	return (*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(data)))
}

func operationName(operation uint32) string {
	names := [...]string{"save", "convert", "compress", "decompress", "generate mipmaps", "change alpha mode"}
	return names[operation]
}

func compatibleFormats(source, target Format) bool {
	return C.dds_typeless(C.uint32_t(source)) == C.dds_typeless(C.uint32_t(target))
}
