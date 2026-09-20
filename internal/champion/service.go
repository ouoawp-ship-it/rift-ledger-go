package champion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"riftledger/internal/runtimeconfig"
	"sort"
	"sync"
	"time"
)

type Champion struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	EnglishName string `json:"english_name"`
	Title       string `json:"title"`
	ImageURL    string `json:"image_url"`
	CachePath   string `json:"cache_path"`
}
type Snapshot struct {
	Version   string     `json:"version"`
	UpdatedAt int64      `json:"updated_at"`
	Champions []Champion `json:"champions"`
	Status    string     `json:"status"`
}
type ChampionService struct {
	mu           sync.RWMutex
	refresh      sync.Mutex
	Dir, BaseURL string
	HTTP         *http.Client
	snapshot     Snapshot
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9]+$`)
var safeVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func New(dir string) (*ChampionService, error) {
	s := &ChampionService{Dir: dir, BaseURL: "https://ddragon.leagueoflegends.com", HTTP: &http.Client{Timeout: 20 * time.Second}, snapshot: Snapshot{Champions: []Champion{}, Status: "尚未缓存，请刷新英雄数据"}}
	b, e := os.ReadFile(filepath.Join(dir, "champions.json"))
	if os.IsNotExist(e) {
		return s, nil
	}
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(b, &s.snapshot); e != nil {
		return nil, errors.New("英雄缓存格式损坏")
	}
	s.snapshot.Status = "使用本地缓存"
	return s, nil
}
func (s *ChampionService) View() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := s.snapshot
	v.Champions = append([]Champion{}, v.Champions...)
	return v
}
func (s *ChampionService) Lookup(id string) (Champion, bool) {
	for _, c := range s.View().Champions {
		if c.ID == id {
			return c, true
		}
	}
	return Champion{}, false
}
func (s *ChampionService) Image(version, id string) ([]byte, error) {
	if !safeVersion.MatchString(version) || !safeID.MatchString(id) {
		return nil, errors.New("无效英雄图片")
	}
	return os.ReadFile(filepath.Join(s.Dir, version, id+".png"))
}
func (s *ChampionService) get(ctx context.Context, path string) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", s.BaseURL+path, nil)
	if e != nil {
		return nil, e
	}
	res, e := s.HTTP.Do(req)
	if e != nil {
		return nil, errors.New("Riot网络连接失败")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("Riot响应状态%d", res.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if len(b) >= 8<<20 {
		return nil, errors.New("Riot响应过大")
	}
	return b, e
}
func (s *ChampionService) Refresh(ctx context.Context) (Snapshot, error) {
	if !s.refresh.TryLock() {
		return s.View(), errors.New("英雄数据正在刷新")
	}
	defer s.refresh.Unlock()
	snap, e := s.download(ctx)
	if e != nil {
		s.mu.Lock()
		s.snapshot.Status = "刷新失败，保留本地缓存"
		s.mu.Unlock()
		return s.View(), e
	}
	b, _ := json.Marshal(snap)
	if e = runtimeconfig.AtomicWrite(filepath.Join(s.Dir, "champions.json"), b); e != nil {
		return s.View(), e
	}
	s.mu.Lock()
	s.snapshot = snap
	s.mu.Unlock()
	return s.View(), nil
}
func (s *ChampionService) download(ctx context.Context) (Snapshot, error) {
	empty := Snapshot{}
	b, e := s.get(ctx, "/api/versions.json")
	if e != nil {
		return empty, e
	}
	var versions []string
	if json.Unmarshal(b, &versions) != nil || len(versions) == 0 || !safeVersion.MatchString(versions[0]) {
		return empty, errors.New("Riot版本数据无效")
	}
	version := versions[0]
	type record struct{ ID, Key, Name, Title string }
	var zh, en struct{ Data map[string]record }
	for _, lang := range []string{"zh_CN", "en_US"} {
		b, e = s.get(ctx, "/cdn/"+version+"/data/"+lang+"/champion.json")
		if e != nil {
			return empty, e
		}
		target := &zh
		if lang == "en_US" {
			target = &en
		}
		if json.Unmarshal(b, target) != nil {
			return empty, errors.New("Riot英雄数据无效")
		}
	}
	if len(zh.Data) == 0 {
		return empty, errors.New("Riot返回空英雄列表")
	}
	if e = os.MkdirAll(filepath.Join(s.Dir, version), 0700); e != nil {
		return empty, e
	}
	snap := Snapshot{Version: version, UpdatedAt: time.Now().Unix(), Status: "缓存就绪", Champions: []Champion{}}
	for _, c := range zh.Data {
		if !safeID.MatchString(c.ID) || c.Key == "" || c.Name == "" || en.Data[c.ID].Name == "" {
			return empty, errors.New("Riot英雄字段无效")
		}
		snap.Champions = append(snap.Champions, Champion{ID: c.ID, Key: c.Key, Name: c.Name, EnglishName: en.Data[c.ID].Name, Title: c.Title, ImageURL: s.BaseURL + "/cdn/" + version + "/img/champion/" + c.ID + ".png", CachePath: version + "/" + c.ID + ".png"})
	}
	// Four workers bound refresh time and traffic; publish only a complete snapshot.
	jobs := make(chan Champion, len(snap.Champions))
	errs := make(chan error, len(snap.Champions))
	var wg sync.WaitGroup
	for _, c := range snap.Champions {
		jobs <- c
	}
	close(jobs)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				path := filepath.Join(s.Dir, c.CachePath)
				if b, e := os.ReadFile(path); e == nil && validPNG(b) {
					continue
				}
				b, e := s.get(ctx, "/cdn/"+version+"/img/champion/"+c.ID+".png")
				if e == nil && !validPNG(b) {
					e = errors.New("英雄头像不是有效PNG")
				}
				if e == nil {
					e = runtimeconfig.AtomicWrite(path, b)
				}
				if e != nil {
					errs <- e
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		return empty, e
	}
	sort.Slice(snap.Champions, func(i, j int) bool { return snap.Champions[i].ID < snap.Champions[j].ID })
	return snap, nil
}
func validPNG(b []byte) bool { return len(b) > 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n" }
