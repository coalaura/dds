#include "bridge.h"
#include "internal/native/DirectXTex/DirectXTex/DirectXTexP.h"
#include <new>

using namespace DirectX;

struct dds_result {
	ScratchImage image;
	Blob blob;
};

static TexMetadata metadata_in(const dds_metadata& input) {
	return {input.width, input.height, input.depth, input.arrays, input.mips, input.misc, input.misc2, static_cast<DXGI_FORMAT>(input.format), static_cast<TEX_DIMENSION>(input.dimension)};
}

static void metadata_out(const TexMetadata& input, dds_metadata& output) {
	output = {input.width, input.height, input.depth, input.arraySize, input.mipLevels, static_cast<uint32_t>(input.format), static_cast<uint32_t>(input.dimension), input.miscFlags, input.miscFlags2};
}

extern "C" uint32_t dds_inspect(const uint8_t* data, size_t size, uint32_t flags, dds_metadata* metadata) {
	TexMetadata native{};
	HRESULT status = GetMetadataFromDDSMemory(data, size, static_cast<DDS_FLAGS>(flags), native);
	if (SUCCEEDED(status)) {
		metadata_out(native, *metadata);
	}
	return static_cast<uint32_t>(status);
}

extern "C" uint32_t dds_pitch(uint32_t format, size_t width, size_t height, size_t* row, size_t* slice) {
	return static_cast<uint32_t>(ComputePitch(static_cast<DXGI_FORMAT>(format), width, height, *row, *slice));
}

extern "C" uint32_t dds_typeless(uint32_t format) {
	return static_cast<uint32_t>(MakeTypeless(static_cast<DXGI_FORMAT>(format)));
}

extern "C" uint32_t dds_format_info(uint32_t value, uint32_t* bits, uint32_t* precision, uint32_t* kind) {
	auto format = static_cast<DXGI_FORMAT>(value);
	*bits = static_cast<uint32_t>(BitsPerPixel(format));
	*precision = static_cast<uint32_t>(BitsPerColor(format));
	*kind = static_cast<uint32_t>(FormatDataType(format));
	return (IsValid(format) ? 1u : 0u) | (IsCompressed(format) ? 2u : 0u) | (IsTypeless(format) ? 4u : 0u) | (IsSRGB(format) ? 8u : 0u) | (HasAlpha(format) ? 16u : 0u) | (IsPlanar(format) ? 32u : 0u) | (IsVideo(format) ? 64u : 0u);
}

extern "C" uint32_t dds_load(const uint8_t* data, size_t size, uint32_t flags, dds_result** output) {
	auto result = std::unique_ptr<dds_result>(new (std::nothrow) dds_result);
	if (!result) {
		return static_cast<uint32_t>(E_OUTOFMEMORY);
	}
	HRESULT status = LoadFromDDSMemory(data, size, static_cast<DDS_FLAGS>(flags), nullptr, result->image);
	if (SUCCEEDED(status)) {
		*output = result.release();
	}
	return static_cast<uint32_t>(status);
}

// Borrowed pointers are valid for this call only. Exceptions never cross CGO.
extern "C" uint32_t dds_process(const dds_metadata* metadata, const dds_surface* surfaces, size_t count, const uint8_t* pixels, uint32_t operation, uint32_t target, uint32_t flags, float threshold, size_t levels, dds_result** output) {
	try {
		auto result = std::make_unique<dds_result>();
		std::vector<Image> images(count);
		TexMetadata native = metadata_in(*metadata);
		for (size_t index = 0; index < count; ++index) {
			const auto& surface = surfaces[index];
			images[index] = {surface.width, surface.height, native.format, surface.row, surface.slice, const_cast<uint8_t*>(pixels + surface.offset)};
		}
		HRESULT status = E_INVALIDARG;
		auto format = static_cast<DXGI_FORMAT>(target);
		switch (operation) {
		case 0:
			status = SaveToDDSMemory(images.data(), count, native, static_cast<DDS_FLAGS>(flags), result->blob);
			break;
		case 1:
			status = Convert(images.data(), count, native, format, static_cast<TEX_FILTER_FLAGS>(flags), threshold, result->image);
			break;
		case 2:
			status = Compress(images.data(), count, native, format, static_cast<TEX_COMPRESS_FLAGS>(flags), threshold, result->image);
			break;
		case 3:
			status = Decompress(images.data(), count, native, format, result->image);
			break;
		case 4:
			if (native.dimension == TEX_DIMENSION_TEXTURE3D) {
				status = GenerateMipMaps3D(images.data(), count, native, static_cast<TEX_FILTER_FLAGS>(flags), levels, result->image);
			} else {
				status = GenerateMipMaps(images.data(), count, native, static_cast<TEX_FILTER_FLAGS>(flags), levels, result->image);
			}
			break;
		case 5:
			status = PremultiplyAlpha(images.data(), count, native, static_cast<TEX_PMALPHA_FLAGS>(flags), result->image);
			break;
		}
		if (SUCCEEDED(status)) {
			*output = result.release();
		}
		return static_cast<uint32_t>(status);
	} catch (const std::bad_alloc&) {
		return static_cast<uint32_t>(E_OUTOFMEMORY);
	} catch (...) {
		return static_cast<uint32_t>(E_FAIL);
	}
}

extern "C" void dds_result_info(const dds_result* result, dds_metadata* metadata, size_t* count, const uint8_t** pixels, size_t* size) {
	metadata_out(result->image.GetMetadata(), *metadata);
	*count = result->image.GetImageCount();
	if (*count) {
		*pixels = result->image.GetPixels();
		*size = result->image.GetPixelsSize();
	} else {
		*pixels = result->blob.GetConstBufferPointer();
		*size = result->blob.GetBufferSize();
	}
}

extern "C" void dds_result_surfaces(const dds_result* result, dds_surface* surfaces) {
	for (size_t index = 0; index < result->image.GetImageCount(); ++index) {
		const auto& image = result->image.GetImages()[index];
		surfaces[index] = {image.width, image.height, image.rowPitch, image.slicePitch, static_cast<size_t>(image.pixels - result->image.GetPixels())};
	}
}

extern "C" void dds_release(dds_result* result) {
	delete result;
}
