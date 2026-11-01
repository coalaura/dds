package dds

import (
	"encoding/binary"
	"fmt"
	"math"
)

// transformColor operates only on an exclusively owned RGBA32_FLOAT temporary.
// Association is in the supplied sample domain: unassociate before changing
// transfer functions, then associate in the destination domain. Native sRGB
// conversion instead transfers already linear-associated samples.
func transformColor(texture *Texture, sourceSRGB, targetSRGB bool, sourceAlpha, targetAlpha AlphaMode) {
	for index := range texture.surfaces {
		surface := &texture.surfaces[index]

		for row := range surface.Height {
			pixels := texture.pixels[surface.offset+row*surface.RowPitch:]

			for column := range surface.Width {
				pixel := pixels[column*16 : column*16+16]
				alpha := float64(math.Float32frombits(binary.LittleEndian.Uint32(pixel[12:])))

				for channel := range 3 {
					sample := pixel[channel*4:]
					value := float64(math.Float32frombits(binary.LittleEndian.Uint32(sample)))

					if sourceAlpha == AlphaPremultiplied {
						if alpha != 0 {
							value /= alpha
						} else {
							value = 0
						}
					}

					if sourceSRGB != targetSRGB {
						if sourceSRGB {
							value = decodeSRGB(value)
						} else {
							value = encodeSRGB(value)
						}
					}

					if targetAlpha == AlphaPremultiplied {
						value *= alpha
					}

					binary.LittleEndian.PutUint32(sample, math.Float32bits(float32(value)))
				}
			}
		}
	}

	texture.metadata.MiscFlags2 = texture.metadata.MiscFlags2&^7 | uint32(targetAlpha)
}

// floatCopy always owns its storage, even when the input is already float.
func floatCopy(texture *Texture, filter FilterFlags) (*Texture, error) {
	err := texture.outputCheck(FormatR32G32B32A32_FLOAT, texture.metadata.MipLevels)
	if err != nil {
		return nil, err
	}

	if texture.metadata.Format == FormatR32G32B32A32_FLOAT && filter&^FilterSRGBIn == 0 {
		result := *texture
		result.pixels = append([]byte(nil), texture.pixels...)
		result.original = nil

		if filter&FilterSRGBIn != 0 {
			transformColor(&result, true, false, AlphaStraight, AlphaStraight)
			result.metadata.MiscFlags2 = texture.metadata.MiscFlags2
		}

		return &result, nil
	}

	return texture.Convert(FormatR32G32B32A32_FLOAT, &ConversionOptions{Filter: filter})
}

// A normalized sRGB premultiplied white can round above alpha in storage:
// E(128/255) rounds to 188/255, whose decoded value exceeds 128/255.
// Recognize only this representable endpoint, not arbitrary RGB > alpha.
// All supported typed sRGB layouts (including decompressed BC) have 8-bit channels.
// Validate before unassociation can erase invalid RGB at zero alpha.
func checkSRGBPremultiplication(texture *Texture, display DisplayMode) error {
	for index := range texture.surfaces {
		surface := &texture.surfaces[index]

		for row := range surface.Height {
			pixels := texture.pixels[surface.offset+row*surface.RowPitch:]

			for column := range surface.Width {
				pixel := pixels[column*16 : column*16+16]
				alpha := math.Float32frombits(binary.LittleEndian.Uint32(pixel[12:]))

				for channel := range 3 {
					sample := pixel[channel*4:]

					value := math.Float32frombits(binary.LittleEndian.Uint32(sample))
					if value <= alpha {
						continue
					}

					// Compare in the original 8-bit sample domain to avoid depending
					// on the last few bits of DirectXMath's transfer approximation.
					stored := math.Round(encodeSRGB(float64(value)) * 255)
					maximum := math.Round(encodeSRGB(float64(alpha)) * 255)

					if stored <= maximum {
						binary.LittleEndian.PutUint32(sample, math.Float32bits(alpha))
					} else if display != DisplayClamp {
						return fmt.Errorf("dds: premultiplied RGB exceeds alpha; use DisplayClamp or correct the alpha interpretation")
					}
				}
			}
		}
	}

	return nil
}

func decodeSRGB(value float64) float64 {
	if value <= 0.04045 {
		return value / 12.92
	}

	return math.Pow((value+0.055)/1.055, 2.4)
}

func encodeSRGB(value float64) float64 {
	if value <= 0.0031308 {
		return value * 12.92
	}

	return 1.055*math.Pow(value, 1/2.4) - 0.055
}
