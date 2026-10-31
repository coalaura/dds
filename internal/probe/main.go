package main

import (
	"github.com/coalaura/dds"
	"image"
	"io"
)

func main() {
	_ = dds.Encode(io.Discard, image.NewNRGBA(image.Rect(0, 0, 4, 4)), nil)
}
