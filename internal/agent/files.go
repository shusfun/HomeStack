package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
)

type FileService struct {
	roots map[string]protocol.SharedDirectory
}
type FileItem struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Type     string    `json:"type"`
	Path     string    `json:"path,omitempty"`
}
type FileResource struct {
	FileItem
	Files   []FileItem `json:"files"`
	Folders []FileItem `json:"folders"`
}
type FileSearchResult struct {
	FileItem
}

func NewFileService(directories []protocol.SharedDirectory) *FileService {
	roots := make(map[string]protocol.SharedDirectory, len(directories))
	for _, directory := range directories {
		roots[directory.ID] = directory
	}
	return &FileService{roots: roots}
}

func (s *FileService) Health() error {
	ids := make([]string, 0, len(s.roots))
	for id := range s.roots {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		root := s.roots[id]
		resolved, err := filepath.EvalSymlinks(root.Path)
		if err != nil {
			return fmt.Errorf("共享目录 %s 不可用: %w", root.Name, err)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return fmt.Errorf("共享目录 %s 不可用: %w", root.Name, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("共享目录 %s 不再是目录", root.Name)
		}
	}
	return nil
}

func (s *FileService) List(virtual string) (FileResource, error) {
	virtual = cleanVirtual(virtual)
	if virtual == "/" {
		folders := make([]FileItem, 0, len(s.roots))
		for id, root := range s.roots {
			folders = append(folders, FileItem{Name: root.Name, Type: "directory", Path: "/" + id})
		}
		sort.Slice(folders, func(i, j int) bool { return folders[i].Name < folders[j].Name })
		return FileResource{FileItem: FileItem{Name: "共享目录", Type: "directory", Path: "/"}, Files: []FileItem{}, Folders: folders}, nil
	}
	path, info, err := s.resolve(virtual)
	if err != nil {
		return FileResource{}, err
	}
	if !info.IsDir() {
		return FileResource{}, errors.New("目标不是目录")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return FileResource{}, err
	}
	resource := FileResource{FileItem: fileItem(info), Files: []FileItem{}, Folders: []FileItem{}}
	resource.Path = virtual
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		entryPath := filepath.Join(path, entry.Name())
		target, err := filepath.EvalSymlinks(entryPath)
		if err != nil {
			continue
		}
		entryInfo, err := os.Stat(target)
		if err != nil {
			continue
		}
		item := fileItem(entryInfo)
		item.Name = entry.Name()
		if entryInfo.IsDir() {
			resource.Folders = append(resource.Folders, item)
		} else if entryInfo.Mode().IsRegular() {
			resource.Files = append(resource.Files, item)
		}
	}
	sort.Slice(resource.Folders, func(i, j int) bool { return resource.Folders[i].Name < resource.Folders[j].Name })
	sort.Slice(resource.Files, func(i, j int) bool { return resource.Files[i].Name < resource.Files[j].Name })
	return resource, nil
}

func (s *FileService) Search(query string, limit int) ([]FileSearchResult, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" || len(query) > 200 || limit < 1 || limit > 200 {
		return nil, errors.New("搜索关键词或结果数量无效")
	}
	results := make([]FileSearchResult, 0, limit)
	visited := 0
	for id, root := range s.roots {
		rootPath, err := filepath.EvalSymlinks(root.Path)
		if err != nil {
			return nil, err
		}
		err = filepath.WalkDir(rootPath, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			visited++
			if visited > 20_000 || len(results) >= limit {
				return fs.SkipAll
			}
			if path == rootPath {
				return nil
			}
			if strings.HasPrefix(entry.Name(), ".") {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 || !strings.Contains(strings.ToLower(entry.Name()), query) {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(rootPath, path)
			if err != nil {
				return err
			}
			item := fileItem(info)
			item.Path = "/" + id + "/" + filepath.ToSlash(relative)
			results = append(results, FileSearchResult{FileItem: item})
			return nil
		})
		if err != nil {
			return nil, err
		}
		if len(results) >= limit || visited > 20_000 {
			break
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return strings.ToLower(results[i].Path) < strings.ToLower(results[j].Path) })
	return results, nil
}

func (s *FileService) ResolveFile(virtual string) (string, fs.FileInfo, error) {
	path, info, err := s.resolve(cleanVirtual(virtual))
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, errors.New("只允许读取普通文件")
	}
	return path, info, nil
}

func (s *FileService) resolve(virtual string) (string, fs.FileInfo, error) {
	parts := strings.Split(strings.TrimPrefix(virtual, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", nil, errors.New("共享路径无效")
	}
	for _, part := range parts {
		if strings.HasPrefix(part, ".") {
			return "", nil, errors.New("拒绝访问隐藏路径")
		}
	}
	root, ok := s.roots[parts[0]]
	if !ok {
		return "", nil, errors.New("共享目录不存在")
	}
	rootPath, err := filepath.EvalSymlinks(root.Path)
	if err != nil {
		return "", nil, err
	}
	target := filepath.Join(append([]string{rootPath}, parts[1:]...)...)
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", nil, err
	}
	relative, err := filepath.Rel(rootPath, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", nil, errors.New("共享路径越界")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, err
	}
	if info.Mode()&(os.ModeDevice|os.ModeNamedPipe|os.ModeSocket) != 0 {
		return "", nil, errors.New("拒绝读取特殊设备文件")
	}
	return resolved, info, nil
}

func cleanVirtual(value string) string {
	if strings.TrimSpace(value) == "" {
		return "/"
	}
	return filepath.ToSlash(filepath.Clean("/" + strings.TrimSpace(value)))
}
func fileItem(info fs.FileInfo) FileItem {
	kind := "file"
	if info.IsDir() {
		kind = "directory"
	}
	return FileItem{Name: info.Name(), Size: info.Size(), Modified: info.ModTime(), Type: kind}
}
