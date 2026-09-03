package waspack

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ResolveBatch 把清单映射到本地批量工程（直接相对工程根目录），可选补 java↔class（含内部类）。
func ResolveBatch(projectDir string, listed []Entry, autoPair bool) (*Preview, error) {
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

	pv := &Preview{Files: make([]ResolvedFile, 0, len(listed)), Warnings: nil}
	seen := map[string]struct{}{}
	add := func(rel, source string) {
		if _, ok := seen[rel]; ok {
			return
		}
		seen[rel] = struct{}{}
		rf := locateBatch(absProject, rel, source)
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
		for _, extra := range pairOfBatch(absProject, e) {
			add(extra.Rel, extra.Source)
		}
	}

	if pv.Stats.Total == 0 && len(pv.Missing) > 0 {
		return pv, fmt.Errorf("清单中的文件在批量工程目录里一个都找不到")
	}
	return pv, nil
}

func locateBatch(projectDir, rel, source string) ResolvedFile {
	clean := strings.TrimPrefix(strings.ReplaceAll(rel, "\\", "/"), "./")
	clean = strings.TrimLeft(clean, "/")
	abs := filepath.Join(projectDir, filepath.FromSlash(clean))
	rf := ResolvedFile{Rel: "./" + clean, Abs: abs, Kind: kindOf(clean), Source: source}
	st, err := os.Lstat(abs)
	if err != nil || st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return rf
	}
	rf.Exists = true
	rf.Bytes = st.Size()
	return rf
}

func pairOfBatch(projectDir string, e Entry) []Entry {
	switch e.Kind {
	case KindJava:
		return classFilesForJavaBatch(projectDir, e.Rel)
	case KindClass:
		return javaAndInnersForClassBatch(projectDir, e.Rel)
	default:
		return nil
	}
}

func classFilesForJavaBatch(projectDir, javaRel string) []Entry {
	rel := strings.TrimPrefix(strings.ReplaceAll(javaRel, "\\", "/"), "./")
	if !strings.HasSuffix(rel, ".java") {
		return nil
	}
	idx := strings.Index(rel, "/src/")
	var module, pkgFile string
	if idx >= 0 {
		module = rel[:idx]
		pkgFile = rel[idx+len("/src/"):]
	} else if strings.HasPrefix(rel, "src/") {
		module = ""
		pkgFile = strings.TrimPrefix(rel, "src/")
	} else {
		return nil
	}

	base := strings.TrimSuffix(path.Base(pkgFile), ".java")
	pkgDir := path.Dir(pkgFile)

	var classDirRel string
	if module != "" {
		classDirRel = module + "/classes"
	} else {
		classDirRel = "classes"
	}
	if pkgDir != "." {
		classDirRel = path.Join(classDirRel, pkgDir)
	}
	absDir := filepath.Join(projectDir, filepath.FromSlash(classDirRel))
	return scanClassDir(absDir, classDirRel, base)
}

func javaAndInnersForClassBatch(projectDir, classRel string) []Entry {
	rel := strings.TrimPrefix(strings.ReplaceAll(classRel, "\\", "/"), "./")
	if !strings.HasSuffix(rel, ".class") {
		return nil
	}
	idx := strings.Index(rel, "/classes/")
	var module, pkgFile string
	if idx >= 0 {
		module = rel[:idx]
		pkgFile = rel[idx+len("/classes/"):]
	} else if strings.HasPrefix(rel, "classes/") {
		module = ""
		pkgFile = strings.TrimPrefix(rel, "classes/")
	} else {
		return nil
	}

	name := strings.TrimSuffix(path.Base(pkgFile), ".class")
	if strings.Contains(name, "$") {
		return nil
	}

	var javaRel string
	if module != "" {
		javaRel = "./" + module + "/src/" + strings.TrimSuffix(pkgFile, ".class") + ".java"
	} else {
		javaRel = "./src/" + strings.TrimSuffix(pkgFile, ".class") + ".java"
	}

	out := []Entry{{Rel: javaRel, Kind: KindJava, Source: "paired"}}
	pkgDir := path.Dir(pkgFile)
	var classDirRel string
	if module != "" {
		classDirRel = module + "/classes"
	} else {
		classDirRel = "classes"
	}
	if pkgDir != "." {
		classDirRel = path.Join(classDirRel, pkgDir)
	}
	absDir := filepath.Join(projectDir, filepath.FromSlash(classDirRel))
	out = append(out, scanClassDir(absDir, classDirRel, name)...)
	return out
}
