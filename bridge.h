#ifndef DDS_BRIDGE_H
#define DDS_BRIDGE_H
#include <stddef.h>
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif
typedef struct {
	size_t width, height, depth, arrays, mips;
	uint32_t format, dimension, misc, misc2;
} dds_metadata;
typedef struct {
	size_t width, height, row, slice, offset;
} dds_surface;
typedef struct dds_result dds_result;
uint32_t dds_inspect(const uint8_t*, size_t, uint32_t, dds_metadata*);
uint32_t dds_pitch(uint32_t, size_t, size_t, size_t*, size_t*);
uint32_t dds_format_info(uint32_t, uint32_t*, uint32_t*, uint32_t*);
uint32_t dds_typeless(uint32_t);
uint32_t dds_load(const uint8_t*, size_t, uint32_t, dds_result**);
uint32_t dds_process(const dds_metadata*, const dds_surface*, size_t, const uint8_t*, uint32_t, uint32_t, uint32_t, float, size_t, dds_result**);
void dds_result_info(const dds_result*, dds_metadata*, size_t*, const uint8_t**, size_t*);
void dds_result_surfaces(const dds_result*, dds_surface*);
void dds_release(dds_result*);
#ifdef __cplusplus
}
#endif
#endif
