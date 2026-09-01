package waspack

import (
	"os"
	"path/filepath"
	"strings"
)

// Layout 描述本地 credit 工程怎么落到清单相对路径。
//
// 用户只选工程根（例如 C:\ideaSpaces\credit）：
//
//	src/                ← ./src/...java
//	WebRoot/            ← JSP（./CreditManage/...）以及 ./WEB-INF/...
//	WebRoot/WEB-INF/    ← class 在 WEB-INF/classes
//
// 若工程根本身就是 exploded WAR（根下直接有 WEB-INF、src），WebRoot 等于工程根。
type Layout struct {
	Root       string
	SrcDir     string
	WebRoot    string
	ClassesDir string
}

func DetectLayout(root string) Layout {
	root = filepath.Clean(root)
	webRoot := root
	if found, ok := childDir(root, "WebRoot"); ok {
		webRoot = found
	} else if isDir(filepath.Join(root, "WEB-INF")) && isDir(filepath.Join(filepath.Dir(root), "src")) && looksLikeWebRootName(filepath.Base(root)) {
		// 误选了 WebRoot 本身：上一级才是工程根。
		root = filepath.Dir(root)
	}
	return Layout{
		Root:       root,
		SrcDir:     filepath.Join(root, "src"),
		WebRoot:    webRoot,
		ClassesDir: filepath.Join(webRoot, "WEB-INF", "classes"),
	}
}

func looksLikeWebRootName(name string) bool {
	return strings.EqualFold(name, "WebRoot")
}

func childDir(parent, name string) (string, bool) {
	exact := filepath.Join(parent, name)
	if isDir(exact) {
		return exact, true
	}
	ents, err := os.ReadDir(parent)
	if err != nil {
		return "", false
	}
	for _, ent := range ents {
		if !ent.IsDir() {
			continue
		}
		if strings.EqualFold(ent.Name(), name) {
			p := filepath.Join(parent, ent.Name())
			return p, true
		}
	}
	return "", false
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// Abs 把清单相对路径（./src/...、./WEB-INF/...、./CreditManage/...）映射到本地绝对路径。
func (l Layout) Abs(rel string) string {
	clean := strings.TrimPrefix(strings.ReplaceAll(rel, "\\", "/"), "./")
	clean = strings.TrimLeft(clean, "/")
	if strings.HasPrefix(clean, "src/") || clean == "src" {
		return filepath.Join(l.Root, filepath.FromSlash(clean))
	}
	return filepath.Join(l.WebRoot, filepath.FromSlash(clean))
}

func (l Layout) warnings() []string {
	var out []string
	if !isDir(l.SrcDir) {
		out = append(out, "工程根下没有 src，java 将无法抽取")
	}
	if !isDir(filepath.Join(l.WebRoot, "WEB-INF")) {
		out = append(out, "没有找到 WebRoot/WEB-INF（或工程根下的 WEB-INF），jsp / class 将无法抽取")
	}
	return out
}
