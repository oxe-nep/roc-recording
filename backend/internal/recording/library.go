package recording

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const DefaultCategory = "_unsorted"

type CategoryInfo struct {
	Name      string `json:"name"`
	FileCount int    `json:"file_count"`
}

type LibraryFile struct {
	Category string    `json:"category"`
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
	URL      string    `json:"url"`
}

func (m *Manager) EnsureLibrary() error {
	if err := os.MkdirAll(m.recordingDir, 0o755); err != nil {
		return err
	}
	return os.MkdirAll(filepath.Join(m.recordingDir, DefaultCategory), 0o755)
}

func (m *Manager) loadChannelCategories() {
	if m.categoryAssignPath == "" {
		return
	}
	data, err := os.ReadFile(m.categoryAssignPath)
	if err != nil {
		return
	}
	var asg map[string]string
	if err := json.Unmarshal(data, &asg); err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for idStr, cat := range asg {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		st, ok := m.states[id]
		if !ok {
			continue
		}
		clean := sanitizeCategory(cat)
		if clean == "" {
			continue
		}
		st.mu.Lock()
		st.category = clean
		st.mu.Unlock()
	}
}

func (m *Manager) saveChannelCategoriesLocked() error {
	if m.categoryAssignPath == "" {
		return nil
	}
	asg := make(map[string]string, len(m.states))
	for id, st := range m.states {
		st.mu.Lock()
		asg[fmt.Sprintf("%d", id)] = st.category
		st.mu.Unlock()
	}
	data, err := json.MarshalIndent(asg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.categoryAssignPath, data, 0o644)
}

func (m *Manager) ListCategories() ([]CategoryInfo, error) {
	if err := m.EnsureLibrary(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(m.recordingDir)
	if err != nil {
		return nil, err
	}
	out := make([]CategoryInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		count := 0
		files, err := os.ReadDir(filepath.Join(m.recordingDir, name))
		if err == nil {
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(strings.ToLower(f.Name()), ".mp4") {
					count++
				}
			}
		}
		out = append(out, CategoryInfo{Name: name, FileCount: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == DefaultCategory {
			return true
		}
		if out[j].Name == DefaultCategory {
			return false
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (m *Manager) CreateCategory(name string) (CategoryInfo, error) {
	clean := sanitizeCategory(name)
	if clean == "" {
		return CategoryInfo{}, fmt.Errorf("invalid category name")
	}
	if err := m.EnsureLibrary(); err != nil {
		return CategoryInfo{}, err
	}
	path := filepath.Join(m.recordingDir, clean)
	if err := os.Mkdir(path, 0o755); err != nil {
		if os.IsExist(err) {
			return CategoryInfo{}, fmt.Errorf("category already exists")
		}
		return CategoryInfo{}, err
	}
	return CategoryInfo{Name: clean, FileCount: 0}, nil
}

func (m *Manager) RenameCategory(oldName, newName string) (CategoryInfo, error) {
	oldClean := sanitizeCategory(oldName)
	newClean := sanitizeCategory(newName)
	if oldClean == "" || newClean == "" {
		return CategoryInfo{}, fmt.Errorf("invalid category name")
	}
	if oldClean == DefaultCategory {
		return CategoryInfo{}, fmt.Errorf("cannot rename default category %s", DefaultCategory)
	}
	if oldClean == newClean {
		cats, _ := m.ListCategories()
		for _, c := range cats {
			if c.Name == oldClean {
				return c, nil
			}
		}
		return CategoryInfo{Name: oldClean}, nil
	}
	oldPath := filepath.Join(m.recordingDir, oldClean)
	newPath := filepath.Join(m.recordingDir, newClean)
	if _, err := os.Stat(oldPath); err != nil {
		return CategoryInfo{}, fmt.Errorf("category not found")
	}
	if _, err := os.Stat(newPath); err == nil {
		return CategoryInfo{}, fmt.Errorf("target category already exists")
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return CategoryInfo{}, err
	}
	// Update channels pointing at old category.
	m.mu.Lock()
	for _, st := range m.states {
		st.mu.Lock()
		if st.category == oldClean {
			st.category = newClean
		}
		st.mu.Unlock()
	}
	_ = m.saveChannelCategoriesLocked()
	m.mu.Unlock()

	cats, err := m.ListCategories()
	if err != nil {
		return CategoryInfo{Name: newClean}, nil
	}
	for _, c := range cats {
		if c.Name == newClean {
			return c, nil
		}
	}
	return CategoryInfo{Name: newClean}, nil
}

func (m *Manager) DeleteCategory(name string) error {
	clean := sanitizeCategory(name)
	if clean == "" {
		return fmt.Errorf("invalid category name")
	}
	if clean == DefaultCategory {
		return fmt.Errorf("cannot delete default category %s", DefaultCategory)
	}
	path := filepath.Join(m.recordingDir, clean)
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("category not found")
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".mp4") {
			return fmt.Errorf("category is not empty")
		}
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	m.mu.Lock()
	for _, st := range m.states {
		st.mu.Lock()
		if st.category == clean {
			st.category = DefaultCategory
		}
		st.mu.Unlock()
	}
	_ = m.saveChannelCategoriesLocked()
	m.mu.Unlock()
	return nil
}

func (m *Manager) ListLibraryFiles(categoryFilter string) ([]LibraryFile, error) {
	if err := m.EnsureLibrary(); err != nil {
		return nil, err
	}
	cats, err := m.ListCategories()
	if err != nil {
		return nil, err
	}
	filter := sanitizeCategory(categoryFilter)
	out := make([]LibraryFile, 0)
	for _, cat := range cats {
		if filter != "" && cat.Name != filter {
			continue
		}
		dir := filepath.Join(m.recordingDir, cat.Name)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(strings.ToLower(name), ".mp4") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, LibraryFile{
				Category: cat.Name,
				Name:     name,
				Size:     info.Size(),
				ModTime:  info.ModTime(),
				URL:      "/api/library/file/" + cat.Name + "/" + name,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	return out, nil
}

func (m *Manager) LibraryFilePath(category, name string) (string, error) {
	cat := sanitizeCategory(category)
	if cat == "" {
		return "", fmt.Errorf("invalid category")
	}
	if name == "" || name != filepath.Base(name) || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid file name")
	}
	if !strings.HasSuffix(strings.ToLower(name), ".mp4") {
		return "", fmt.Errorf("invalid file name")
	}
	return filepath.Join(m.recordingDir, cat, name), nil
}

func (m *Manager) DeleteLibraryFile(category, name string) error {
	path, err := m.LibraryFilePath(category, name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file not found")
		}
		return err
	}
	return nil
}

func (m *Manager) MoveLibraryFile(fromCat, toCat, name string) (LibraryFile, error) {
	src, err := m.LibraryFilePath(fromCat, name)
	if err != nil {
		return LibraryFile{}, err
	}
	dstCat := sanitizeCategory(toCat)
	if dstCat == "" {
		return LibraryFile{}, fmt.Errorf("invalid target category")
	}
	if err := os.MkdirAll(filepath.Join(m.recordingDir, dstCat), 0o755); err != nil {
		return LibraryFile{}, err
	}
	dst := filepath.Join(m.recordingDir, dstCat, name)
	if src == dst {
		info, err := os.Stat(src)
		if err != nil {
			return LibraryFile{}, err
		}
		return LibraryFile{
			Category: dstCat,
			Name:     name,
			Size:     info.Size(),
			ModTime:  info.ModTime(),
			URL:      "/api/library/file/" + dstCat + "/" + name,
		}, nil
	}
	if _, err := os.Stat(dst); err == nil {
		return LibraryFile{}, fmt.Errorf("file already exists in target category")
	}
	if err := os.Rename(src, dst); err != nil {
		return LibraryFile{}, err
	}
	info, err := os.Stat(dst)
	if err != nil {
		return LibraryFile{}, err
	}
	return LibraryFile{
		Category: dstCat,
		Name:     name,
		Size:     info.Size(),
		ModTime:  info.ModTime(),
		URL:      "/api/library/file/" + dstCat + "/" + name,
	}, nil
}

// sanitizeCategory keeps filesystem-safe category folder names.
func sanitizeCategory(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_-")
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}
