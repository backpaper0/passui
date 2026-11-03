package store

import (
	"sort"
	"strings"
)

// Folder はツリーのフォルダ。ルートは Name も Path も空。
type Folder struct {
	// Name はフォルダの名前（パスの最後の要素）。
	Name string
	// Path はストアからの相対パス（"dev/github"）。
	Path    string
	Folders []*Folder
	Entries []Entry
}

// Entry はツリーの葉。
type Entry struct {
	// Name はエントリの名前（パスの最後の要素）。
	Name string
	// Path はストアからの相対パス（"dev/github/token"）。Backend にはこれを渡す。
	Path string
}

// BuildTree はエントリの一覧（"a/b/c" の形）からツリーを組み立てる。
// フォルダもエントリも名前順に並べる。
func BuildTree(entries []string) *Folder {
	root := &Folder{}
	index := map[string]*Folder{"": root}
	var folder func(path string) *Folder
	folder = func(path string) *Folder {
		if f, ok := index[path]; ok {
			return f
		}
		parentPath, name := splitPath(path)
		parent := folder(parentPath)
		f := &Folder{Name: name, Path: path}
		parent.Folders = append(parent.Folders, f)
		index[path] = f
		return f
	}
	for _, e := range entries {
		dir, name := splitPath(e)
		f := folder(dir)
		f.Entries = append(f.Entries, Entry{Name: name, Path: e})
	}
	for _, f := range index {
		sort.Slice(f.Folders, func(i, j int) bool { return f.Folders[i].Name < f.Folders[j].Name })
		sort.Slice(f.Entries, func(i, j int) bool { return f.Entries[i].Name < f.Entries[j].Name })
	}
	return root
}

// splitPath は "a/b/c" を "a/b" と "c" に分ける。
func splitPath(p string) (dir, name string) {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "", p
	}
	return p[:i], p[i+1:]
}
