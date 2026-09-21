// Package runtimeconfig persists Telegram settings independently of startup secrets.
package runtimeconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Token           string `json:"token"`
	BotUsername     string `json:"bot_username"`
	GroupID         int64  `json:"group_id"`
	AdminID         int64  `json:"admin_id"`
	SupportUsername string `json:"support_username"`
	Enabled         bool   `json:"enabled"`
	Revision        int64  `json:"revision"`
	SavedAt         int64  `json:"saved_at"`
}
type Patch struct {
	Token           string `json:"token"`
	BotUsername     string `json:"bot_username"`
	GroupID         int64  `json:"group_id"`
	AdminID         int64  `json:"admin_id"`
	SupportUsername string `json:"support_username"`
	Enabled         bool   `json:"enabled"`
	Revision        int64  `json:"revision"`
}
type Store struct {
	mu            sync.Mutex
	path          string
	saved, active Config
}

func Open(path string, initial Config) (*Store, error) {
	c := initial
	b, e := os.ReadFile(path)
	if e == nil {
		if json.Unmarshal(b, &c) != nil {
			return nil, errors.New("运行配置文件损坏，未回退到环境变量")
		}
	} else if !os.IsNotExist(e) {
		return nil, errors.New("无法读取运行配置")
	}
	if e = Validate(c); e != nil {
		return nil, e
	}
	if b != nil {
		if e = os.Chmod(path, 0600); e != nil {
			return nil, errors.New("无法保护运行配置权限")
		}
	}
	return &Store{path: path, saved: c, active: c}, nil
}
func Validate(c Config) error {
	name := regexp.MustCompile(`^[A-Za-z0-9_]{5,64}$`)
	for _, v := range []string{c.BotUsername, c.SupportUsername} {
		if v != "" && !name.MatchString(v) {
			return errors.New("Telegram用户名需要5至64位字母、数字或下划线")
		}
	}
	if c.Token != "" && !regexp.MustCompile(`^[0-9]{5,16}:[A-Za-z0-9_-]{20,128}$`).MatchString(c.Token) {
		return errors.New("机器人Token格式不正确")
	}
	if c.Enabled && (c.Token == "" || c.BotUsername == "") {
		return errors.New("启用Telegram必须填写Token和机器人用户名")
	}
	if c.GroupID > 0 || c.GroupID < -4503599627370495 || c.AdminID < 0 || c.AdminID > 4503599627370495 {
		return errors.New("群ID必须为负整数；管理员ID必须为有效非负整数")
	}
	return nil
}
func (s *Store) Current() Config { s.mu.Lock(); defer s.mu.Unlock(); return s.saved }
func (s *Store) View() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.saved
	mask := "未设置"
	if c.Token != "" {
		mask = "******"
	}
	return map[string]any{"token_mask": mask, "bot_username": c.BotUsername, "group_id": c.GroupID, "admin_id": c.AdminID, "support_username": c.SupportUsername, "enabled": c.Enabled, "active_enabled": s.active.Enabled, "revision": c.Revision, "saved_at": c.SavedAt, "restart_required": c != s.active, "active": map[string]any{"bot_username": s.active.BotUsername, "group_id": s.active.GroupID, "admin_id": s.active.AdminID, "support_username": s.active.SupportUsername}}
}
func (s *Store) Save(p Patch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	token := p.Token
	if token == "" {
		token = s.saved.Token
	}
	c := Config{Token: token, BotUsername: strings.TrimPrefix(strings.TrimSpace(p.BotUsername), "@"), GroupID: p.GroupID, AdminID: p.AdminID, SupportUsername: strings.TrimPrefix(strings.TrimSpace(p.SupportUsername), "@"), Enabled: p.Enabled, Revision: s.saved.Revision + 1, SavedAt: time.Now().Unix()}
	compare := c
	compare.Revision, compare.SavedAt = s.saved.Revision, s.saved.SavedAt
	// A lost save response can be retried without erasing the form or restarting
	// again. A genuinely stale/different edit still receives a conflict.
	if compare == s.saved && (p.Revision == s.saved.Revision || p.Revision == s.saved.Revision-1) {
		return nil
	}
	if p.Revision != s.saved.Revision {
		return errors.New("配置已被修改，请刷新后再保存")
	}
	if e := Validate(c); e != nil {
		return e
	}
	if e := os.MkdirAll(filepath.Dir(s.path), 0700); e != nil {
		return errors.New("无法创建配置目录")
	}
	old, _ := json.Marshal(s.saved)
	if e := AtomicWrite(s.path+".bak", old); e != nil {
		return errors.New("配置备份失败，未更改现有配置")
	}
	b, _ := json.Marshal(c)
	if e := AtomicWrite(s.path, b); e != nil {
		return errors.New("配置保存失败，请重新读取确认；未写入不完整配置")
	}
	s.saved = c
	return nil
}

// AtomicWrite fsyncs a private temporary file, renames it, then fsyncs the directory.
func AtomicWrite(path string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".rift-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
