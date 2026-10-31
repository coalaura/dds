// Validate the on-disk layout before ScratchImage allocates the decoded layout.
// Expansion flags mirror CopyImage's source-pitch selection in DirectXTexDDS.cpp.
CP_FLAGS sourceFlags = CP_FLAGS_NONE;
if (flags & DDS_FLAGS_LEGACY_DWORD) {
	sourceFlags |= CP_FLAGS_LEGACY_DWORD;
}
if (flags & DDS_FLAGS_BAD_DXTN_TAILS) {
	sourceFlags |= CP_FLAGS_BAD_DXTN_TAILS;
}
if (convFlags & CONV_FLAGS_EXPAND) {
	if (convFlags & CONV_FLAGS_888) {
		sourceFlags |= CP_FLAGS_24BPP;
	} else if (convFlags & (CONV_FLAGS_565 | CONV_FLAGS_5551 | CONV_FLAGS_4444 | CONV_FLAGS_8332 | CONV_FLAGS_A8P8 | CONV_FLAGS_L16 | CONV_FLAGS_A8L8 | CONV_FLAGS_L6V5U5)) {
		sourceFlags |= CP_FLAGS_16BPP;
	} else if (convFlags & (CONV_FLAGS_44 | CONV_FLAGS_332 | CONV_FLAGS_PAL8 | CONV_FLAGS_L8)) {
		sourceFlags |= CP_FLAGS_8BPP;
	}
}
size_t sourceCount = 0;
size_t sourceBytes = 0;
hr = DetermineImageArray(mdata, sourceFlags, sourceCount, sourceBytes);
if (FAILED(hr)) {
	return hr;
}
if (sourceBytes > remaining && (flags & DDS_FLAGS_PERMISSIVE) && (mdata.miscFlags & TEX_MISC_TEXTURECUBE) && (convFlags & CONV_FLAGS_DX10) && mdata.arraySize % 6 == 0) {
	mdata.arraySize /= 6;
	hr = DetermineImageArray(mdata, sourceFlags, sourceCount, sourceBytes);
	if (FAILED(hr)) {
		return hr;
	}
}
if (sourceBytes > remaining) {
	return HRESULT_E_HANDLE_EOF;
}
