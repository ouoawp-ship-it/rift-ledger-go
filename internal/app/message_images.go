package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"regexp"
	"riftledger/internal/sqlite"
)

const MaxMessageImage = 2 << 20

var imageIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Content-addressed, immutable assets keep queued messages independent of edits.
func (s *Service) SaveMessageImage(raw []byte) (any, error) {
	if len(raw) == 0 || len(raw) > MaxMessageImage {
		return nil, bad("图片不能为空且不能超过2MB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "png" && format != "jpeg") {
		return nil, bad("只支持有效的PNG或JPEG图片")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width+cfg.Height > 10000 || int64(cfg.Width)*int64(cfg.Height) > 16000000 || cfg.Width > 20*cfg.Height || cfg.Height > 20*cfg.Width {
		return nil, bad("图片尺寸过大或比例过长，请缩小后上传")
	}
	if _, _, err = image.Decode(bytes.NewReader(raw)); err != nil {
		return nil, bad("图片文件不完整")
	}
	id := fmt.Sprintf("%x", sha256.Sum256(raw))
	mime := "image/" + format
	err = s.DB.Transaction(func(tx *sqlite.Tx) error {
		_, e := tx.Exec("INSERT OR IGNORE INTO message_images(id,mime,data,created_at) VALUES(?,?,?,?)", id, mime, base64.StdEncoding.EncodeToString(raw), now())
		return e
	})
	return map[string]any{"id": id, "mime": mime, "width": cfg.Width, "height": cfg.Height, "size": len(raw)}, err
}
func (s *Service) MessageImage(id string) ([]byte, string, error) {
	if !imageIDPattern.MatchString(id) {
		return nil, "", notfound("图片不存在")
	}
	var data []byte
	var mime string
	err := s.DB.Read(func(tx *sqlite.Tx) error {
		r, e := tx.One("SELECT mime,data FROM message_images WHERE id=?", id)
		if e != nil {
			return e
		}
		if r == nil {
			return notfound("图片不存在")
		}
		mime = r["mime"]
		data, e = base64.StdEncoding.DecodeString(r["data"])
		return e
	})
	return data, mime, err
}
