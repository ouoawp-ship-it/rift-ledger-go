package telegram

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	stdDraw "image/draw"
	"image/png"
	"os"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"riftledger/internal/app"
)

func composeHeroImage(raw [][]byte, photos []app.MediaPhoto) ([]byte, error) {
	if len(raw) != 5 || len(photos) != 5 { return nil, errors.New("英雄图片数量无效") }
	const tile = 320
	out := image.NewRGBA(image.Rect(0, 0, tile*2, tile*3))
	stdDraw.Draw(out, out.Bounds(), image.NewUniform(color.RGBA{245, 245, 245, 255}), image.Point{}, stdDraw.Src)
	face, err := loadCJKFace()
	if err != nil { return nil, err }
	for i, b := range raw {
		src, err := png.Decode(bytes.NewReader(b)); if err != nil { return nil, err }
		x, y := (i%2)*tile, (i/2)*tile
		xdraw.CatmullRom.Scale(out, image.Rect(x, y, x+tile, y+tile), src, src.Bounds(), xdraw.Over, nil)
		banker := strings.Contains(photos[i].Caption, "【庄】")
		badge := color.RGBA{40, 116, 220, 235}; label := "闲"
		if banker { badge = color.RGBA{210, 45, 45, 240}; label = "庄" }
		stdDraw.Draw(out, image.Rect(x+12, y+12, x+112, y+66), image.NewUniform(badge), image.Point{}, stdDraw.Over)
		d := &font.Drawer{Dst: out, Src: image.NewUniform(color.White), Face: face, Dot: fixed.Point26_6{X: fixed.I(x + 25), Y: fixed.I(y + 51)}}
		d.DrawString(string([]rune{'①','②','③','④','⑤'}[i]) + label)
	}
	var buf bytes.Buffer; if err := png.Encode(&buf, out); err != nil { return nil, err }; return buf.Bytes(), nil
}

func loadCJKFace() (font.Face, error) {
	paths := []string{"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc", "/usr/share/fonts/opentype/noto/NotoSansCJKsc-Regular.otf", "C:\\Windows\\Fonts\\NotoSansSC-VF.ttf"}
	for _, path := range paths {
		b, err := os.ReadFile(path); if err != nil { continue }
		f, err := opentype.Parse(b); if err != nil { continue }
		face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 34, DPI: 72, Hinting: font.HintingFull}); if err == nil { return face, nil }
	}
	return nil, errors.New("中文字体不可用")
}
