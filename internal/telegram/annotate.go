package telegram

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	stdDraw "image/draw"
	"image/png"
	"os"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"riftledger/internal/app"
)

func composeHeroImage(raw [][]byte, photos []app.MediaPhoto) ([]byte, error) {
	if len(raw) != 5 || len(photos) != 5 { return nil, errors.New("英雄图片数量无效") }
	const tile = 320
	out := image.NewRGBA(image.Rect(0, 0, tile*2, tile*3))
	stdDraw.Draw(out, out.Bounds(), image.NewUniform(color.RGBA{245, 245, 245, 255}), image.Point{}, stdDraw.Src)
	face := loadCJKFace()
	for i, b := range raw {
		src, err := png.Decode(bytes.NewReader(b)); if err != nil { return nil, err }
		x, y := (i%2)*tile, (i/2)*tile
		xdraw.CatmullRom.Scale(out, image.Rect(x, y, x+tile, y+tile), src, src.Bounds(), xdraw.Over, nil)
		banker := strings.Contains(photos[i].Caption, "[庄]")
		badge := color.RGBA{40, 116, 220, 235}; label := "闲"
		if banker { badge = color.RGBA{210, 45, 45, 240}; label = "庄" }
		stdDraw.Draw(out, image.Rect(x+12, y+12, x+112, y+66), image.NewUniform(badge), image.Point{}, stdDraw.Over)
		d := &font.Drawer{Dst: out, Src: image.NewUniform(color.White), Face: face, Dot: fixed.Point26_6{X: fixed.I(x + 25), Y: fixed.I(y + 51)}}
		if face == basicfont.Face7x13 { d.DrawString(fmt.Sprintf("%d %s", i+1, label)) } else { d.DrawString(string([]rune{'①','②','③','④','⑤'}[i]) + label) }
	}
	var buf bytes.Buffer; if err := png.Encode(&buf, out); err != nil { return nil, err }; return buf.Bytes(), nil
}

func loadCJKFace() font.Face {
	paths := []string{"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc", "/usr/share/fonts/opentype/noto/NotoSansCJKsc-Regular.otf", "C:\\Windows\\Fonts\\NotoSansSC-VF.ttf"}
	for _, path := range paths {
		b, err := os.ReadFile(path); if err != nil { continue }
		if collection, err := opentype.ParseCollection(b); err == nil && len(collection) > 0 {
			if face, err := opentype.NewFace(collection[0], &opentype.FaceOptions{Size: 34, DPI: 72, Hinting: font.HintingFull}); err == nil { return face }
		}
		if f, err := opentype.Parse(b); err == nil {
			if face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 34, DPI: 72, Hinting: font.HintingFull}); err == nil { return face }
		}
	}
	return basicfont.Face7x13
}
