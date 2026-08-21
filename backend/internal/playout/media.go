package playout

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxMediaUploadBytes = 16 << 30 // 16 GiB

var allowedMediaExt = map[string]bool{
	".mp4": true,
	".mov": true,
	".mkv": true,
	".mxf": true,
	".ts":  true,
}

type MediaItem struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"-"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

type mediaFile struct {
	Items []persistedMedia `json:"items"`
}

type persistedMedia struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Filename  string    `json:"filename"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

type MediaStore struct {
	mu       sync.Mutex
	dir      string
	metaPath string
	items    map[string]MediaItem
}

func NewMediaStore(dir, metaPath string) *MediaStore {
	s := &MediaStore{
		dir:      dir,
		metaPath: metaPath,
		items:    make(map[string]MediaItem),
	}
	_ = os.MkdirAll(dir, 0o755)
	s.load()
	return s
}

func newMediaID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func (s *MediaStore) load() {
	data, err := os.ReadFile(s.metaPath)
	if err != nil {
		return
	}
	var f mediaFile
	if json.Unmarshal(data, &f) != nil {
		return
	}
	for _, it := range f.Items {
		path := filepath.Join(s.dir, it.Filename)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		s.items[it.ID] = MediaItem{
			ID:        it.ID,
			Name:      it.Name,
			Path:      path,
			Size:      it.Size,
			CreatedAt: it.CreatedAt,
		}
	}
}

func (s *MediaStore) saveLocked() error {
	f := mediaFile{Items: make([]persistedMedia, 0, len(s.items))}
	for _, it := range s.items {
		f.Items = append(f.Items, persistedMedia{
			ID:        it.ID,
			Name:      it.Name,
			Filename:  filepath.Base(it.Path),
			Size:      it.Size,
			CreatedAt: it.CreatedAt,
		})
	}
	sort.Slice(f.Items, func(i, j int) bool {
		return f.Items[i].CreatedAt.After(f.Items[j].CreatedAt)
	})
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.metaPath, data, 0o644)
}

func (s *MediaStore) List() []MediaItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]MediaItem, 0, len(s.items))
	for _, it := range s.items {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func (s *MediaStore) Get(id string) (MediaItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[id]
	return it, ok
}

func (s *MediaStore) Path(id string) (string, error) {
	it, ok := s.Get(id)
	if !ok {
		return "", fmt.Errorf("media %q not found", id)
	}
	if _, err := os.Stat(it.Path); err != nil {
		return "", fmt.Errorf("media file missing on disk")
	}
	return it.Path, nil
}

func (s *MediaStore) Add(origName string, r io.Reader, sizeHint int64) (MediaItem, error) {
	ext := strings.ToLower(filepath.Ext(origName))
	if !allowedMediaExt[ext] {
		return MediaItem{}, fmt.Errorf("unsupported media type %q (allowed: mp4, mov, mkv, mxf, ts)", ext)
	}
	if sizeHint > maxMediaUploadBytes {
		return MediaItem{}, fmt.Errorf("file too large (max %d bytes)", maxMediaUploadBytes)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return MediaItem{}, err
	}

	id := newMediaID()
	safeBase := sanitizeFilename(strings.TrimSuffix(filepath.Base(origName), ext))
	if safeBase == "" {
		safeBase = "media"
	}
	filename := id + "_" + safeBase + ext
	dest := filepath.Join(s.dir, filename)

	f, err := os.Create(dest)
	if err != nil {
		return MediaItem{}, err
	}
	defer f.Close()

	limited := io.LimitReader(r, maxMediaUploadBytes+1)
	n, err := io.Copy(f, limited)
	if err != nil {
		_ = os.Remove(dest)
		return MediaItem{}, err
	}
	if n > maxMediaUploadBytes {
		_ = os.Remove(dest)
		return MediaItem{}, fmt.Errorf("file too large (max %d bytes)", maxMediaUploadBytes)
	}

	item := MediaItem{
		ID:        id,
		Name:      filepath.Base(origName),
		Path:      dest,
		Size:      n,
		CreatedAt: time.Now().UTC(),
	}
	s.mu.Lock()
	s.items[id] = item
	err = s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		_ = os.Remove(dest)
		return MediaItem{}, err
	}
	return item, nil
}

func (s *MediaStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[id]
	if !ok {
		return fmt.Errorf("media %q not found", id)
	}
	delete(s.items, id)
	_ = os.Remove(it.Path)
	return s.saveLocked()
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}
