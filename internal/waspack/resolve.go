package waspack

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ResolvedFile 是工程里真实存在、准备打进 tar 的一条。
type ResolvedFile struct {
	Rel    string   `json:"rel"`
	Abs    string   `json:"abs"`
	Kind   FileKind `json:"kind"`
	Source string   `json:"source"`
	Bytes  int64    `json:"bytes"`
	Exists bool     `json:"exists"`
	info   os.FileInfo
}

// Preview 是预检结果：会打包的文件 + 缺失项 + 告警。
type Preview struct {
	Files    []ResolvedFile `json:"files"`
	Missing  []ResolvedFile `json:"missing"`
	Warnings []string       `json:"warnings"`
	Stats    Stats          `json:"stats"`
}

// Stats 按类型计数。
type Stats struct {
	Total int   `json:"total"`
	Java  int   `json:"java"`
	Class int   `json:"class"`
	JSP   int   `json:"jsp"`
	Other int   `json:"other"`
	Bytes int64 `json:"bytes"`
}

func (s *Stats) add(kind FileKind, n int64) {
	s.Total++
	s.Bytes += n
	switch kind {
	case KindJava:
		s.Java++
	case KindClass:
		s.Class++
	case KindJSP:
		s.JSP++
	default:
		s.Other++
	}
}

// Resolve 把清单映射到本地工程（IntelliJ：src + WebRoot），可选补 java↔class（含内部类）。
func Resolve(projectDir string, listed []Entry, autoPair bool) (*Preview, error) {
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, fmt.Errorf("工程目录无效: %w", err)
	}
	st, err := os.Stat(absProject)
	if err != nil {
		return nil, fmt.Errorf("工程目录不存在: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("工程路径不是目录: %s", absProject)
	}

	layout := DetectLayout(absProject)
	pv := &Preview{Files: make([]ResolvedFile, 0, len(listed)), Warnings: layout.warnings()}
	seen := map[string]struct{}{}
	add := func(rel, source string) {
		if _, ok := seen[rel]; ok {
			return
		}
		seen[rel] = struct{}{}
		rf := locate(layout, rel, source)
		if rf.Exists {
			pv.Files = append(pv.Files, rf)
			pv.Stats.add(rf.Kind, rf.Bytes)
		} else {
			pv.Missing = append(pv.Missing, rf)
		}
	}

	for _, e := range listed {
		add(e.Rel, "listed")
		if !autoPair {
			continue
		}
		for _, extra := range pairOf(layout, e) {
			add(extra.Rel, extra.Source)
		}
	}

	if pv.Stats.Total == 0 {
		return pv, fmt.Errorf("清单中的文件在工程目录里一个都找不到")
	}
	return pv, nil
}

func locate(layout Layout, rel, source string) ResolvedFile {
	abs := layout.Abs(rel)
	rf := ResolvedFile{Rel: rel, Abs: abs, Kind: kindOf(rel), Source: source}
	st, err := os.Lstat(abs)
	if err != nil || st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return rf
	}
	rf.info = st
	rf.Exists = true
	rf.Bytes = st.Size()
	return rf
}

func pairOf(layout Layout, e Entry) []Entry {
	switch e.Kind {
	case KindJava:
		return classFilesForJava(layout, e.Rel)
	case KindClass:
		return javaAndInnersForClass(layout, e.Rel)
	default:
		return nil
	}
}

func classFilesForJava(layout Layout, javaRel string) []Entry {
	rel := strings.TrimPrefix(javaRel, "./")
	if !strings.HasPrefix(rel, "src/") || !strings.HasSuffix(rel, ".java") {
		return nil
	}
	pkgFile := strings.TrimPrefix(rel, "src/")
	base := strings.TrimSuffix(path.Base(pkgFile), ".java")
	pkgDir := path.Dir(pkgFile)
	classDirRel := "WEB-INF/classes"
	if pkgDir != "." {
		classDirRel = path.Join(classDirRel, pkgDir)
	}
	absDir := filepath.Join(layout.ClassesDir, filepath.FromSlash(pkgDir))
	if pkgDir == "." {
		absDir = layout.ClassesDir
	}
	return scanClassDir(absDir, classDirRel, base)
}

func javaAndInnersForClass(layout Layout, classRel string) []Entry {
	rel := strings.TrimPrefix(classRel, "./")
	if !strings.HasPrefix(rel, "WEB-INF/classes/") || !strings.HasSuffix(rel, ".class") {
		return nil
	}
	name := strings.TrimSuffix(path.Base(rel), ".class")
	// A manifest often contains only an inner class (Foo$1.class). Resolve
	// from the outer class name so the source and all sibling inner classes
	// are included as one deployable unit.
	outerName := name
	if i := strings.IndexByte(outerName, '$'); i > 0 {
		outerName = outerName[:i]
	}
	pkgFile := strings.TrimPrefix(rel, "WEB-INF/classes/")
	javaRel := "./src/" + path.Join(path.Dir(pkgFile), outerName+".java")
	out := []Entry{{Rel: javaRel, Kind: KindJava, Source: "paired"}}
	pkgDir := path.Dir(pkgFile)
	classDirRel := "WEB-INF/classes"
	if pkgDir != "." {
		classDirRel = path.Join(classDirRel, pkgDir)
	}
	absDir := filepath.Join(layout.ClassesDir, filepath.FromSlash(pkgDir))
	if pkgDir == "." {
		absDir = layout.ClassesDir
	}
	out = append(out, scanClassDir(absDir, classDirRel, outerName)...)
	return out
}

func scanClassDir(absDir, classDirRel, outerBase string) []Entry {
	ents, err := os.ReadDir(absDir)
	if err != nil {
		rel := "./" + path.Join(classDirRel, outerBase+".class")
		return []Entry{{Rel: rel, Kind: KindClass, Source: "paired"}}
	}
	prefix := outerBase + "$"
	out := make([]Entry, 0, 4)
	for _, ent := range ents {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if name != outerBase+".class" && !(strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".class")) {
			continue
		}
		rel := "./" + path.Join(classDirRel, name)
		out = append(out, Entry{Rel: rel, Kind: KindClass, Source: "paired"})
	}
	if len(out) == 0 {
		rel := "./" + path.Join(classDirRel, outerBase+".class")
		out = append(out, Entry{Rel: rel, Kind: KindClass, Source: "paired"})
	}
	return out
}
