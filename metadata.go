// Package dds reads, writes, and processes DDS textures using bundled DirectXTex.
// Importing dds registers the DDS signature with image.Decode.
// Texture retains native formats, mipmaps, arrays, cube faces, and volume slices.
// All public data is Go-owned; no Close method or native lifetime management is needed.
package dds

const (
	Dimension1D Dimension = 2
	Dimension2D Dimension = 3
	Dimension3D Dimension = 4
)

const (
	AlphaUnknown AlphaMode = iota
	AlphaStraight
	AlphaPremultiplied
	AlphaOpaque
	AlphaCustom
)

const (
	TypeTypeless DataType = iota
	TypeFloat
	TypeUNorm
	TypeSNorm
	TypeUInt
	TypeSInt
)

const (
	FacePositiveX CubeFace = iota
	FaceNegativeX
	FacePositiveY
	FaceNegativeY
	FacePositiveZ
	FaceNegativeZ
)

const (
	defaultMaxBytes = 512 << 20
	maxDimension    = 32768
	maxSubresources = 65536
)

type Dimension uint32
type AlphaMode uint32
type DataType uint32
type CubeFace int

// FormatInfo describes storage, not a guarantee of support for every operation.
type FormatInfo struct {
	Valid        bool
	Compressed   bool
	Typeless     bool
	SRGB         bool
	Alpha        bool
	Planar       bool
	Video        bool
	BitsPerPixel int
	BitsPerColor int
	DataType     DataType
}

// Metadata describes the normalized DirectXTex representation.
// ArraySize counts faces for cubes (six per cube), and is one for volumes.
// MiscFlags and MiscFlags2 preserve supported DDS semantic flags; bit 2 of
// MiscFlags denotes a cube and the low three bits of MiscFlags2 encode alpha mode.
// Color space is specified by Format, not inferred from pixel content.
type Metadata struct {
	Width      int
	Height     int
	Depth      int
	ArraySize  int
	MipLevels  int
	Format     Format
	Dimension  Dimension
	MiscFlags  uint32
	MiscFlags2 uint32
}

// Selection addresses a mip, array element (or individual cube face), and depth slice.
// The zero value selects base mip, first element/face, and first depth slice.
type Selection struct {
	Mip   int
	Item  int
	Slice int
}

// Subresource is one 2D image or volume slice, in native little-endian format.
// Pixels contains SlicePitch bytes, including any row padding.
type Subresource struct {
	Width      int
	Height     int
	RowPitch   int
	SlicePitch int
	Pixels     []byte
	offset     int
}

func (metadata Metadata) IsCube() bool {
	return metadata.MiscFlags&4 != 0
}

func (metadata Metadata) AlphaMode() AlphaMode {
	return AlphaMode(metadata.MiscFlags2 & 7)
}

func (metadata Metadata) IsSRGB() bool {
	return metadata.Format.Info().SRGB
}

// CubeSelection selects a face of a cube array. Surface validates its range.
func CubeSelection(cube int, face CubeFace, mip int) Selection {
	if cube < 0 || cube >= maxSubresources/6 || face < FacePositiveX || face > FaceNegativeZ {
		return Selection{Item: -1}
	}

	return Selection{Mip: mip, Item: cube*6 + int(face)}
}
