// Package note implements Kairo's local sticky-note domain.
package note

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	MaxNotes          = 200
	MaxDesktopVisible = 6
	MaxTitleRunes     = 80
	MaxBodyRunes      = 20_000
	MaxTags           = 8
	MaxTagRunes       = 24
	MaxFolderRunes    = 40
	InboxFolder       = "__inbox__"
)

var allowedColors = map[string]struct{}{
	"yellow": {},
	"blue":   {},
	"green":  {},
	"pink":   {},
	"gray":   {},
}

// DesktopLayout is Windows-only presentation state. Coordinates are stored as
// ratios so a note can be recovered when display geometry changes.
type DesktopLayout struct {
	Visible     bool    `json:"visible"`
	XRatio      float64 `json:"x_ratio"`
	YRatio      float64 `json:"y_ratio"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	Collapsed   bool    `json:"collapsed"`
	MonitorHint string  `json:"monitor_hint,omitempty"`
}

func (l *DesktopLayout) normalize() {
	if l == nil {
		return
	}
	if l.XRatio < 0 {
		l.XRatio = 0
	}
	if l.XRatio > 1 {
		l.XRatio = 1
	}
	if l.YRatio < 0 {
		l.YRatio = 0
	}
	if l.YRatio > 1 {
		l.YRatio = 1
	}
	if l.Width == 0 {
		l.Width = 340
	}
	if l.Height == 0 {
		l.Height = 280
	}
	if l.Width < 240 {
		l.Width = 240
	}
	if l.Width > 1200 {
		l.Width = 1200
	}
	if l.Height < 120 {
		l.Height = 120
	}
	if l.Height > 1000 {
		l.Height = 1000
	}
	l.MonitorHint = strings.TrimSpace(l.MonitorHint)
}

// Note is a persisted note. Revision starts at one and is incremented for
// every successful mutation.
type Note struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Color     string    `json:"color"`
	Folder    string    `json:"folder,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	Pinned    bool      `json:"pinned"`
	Floating  bool      `json:"floating"`
	Archived  bool      `json:"archived"`
	Revision  uint64    `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Desktop *DesktopLayout `json:"desktop,omitempty"`
}

// Normalize applies defaults without discarding meaningful whitespace in Body.
func (n *Note) Normalize() {
	n.Title = strings.TrimSpace(n.Title)
	n.Color = strings.ToLower(strings.TrimSpace(n.Color))
	if n.Color == "" {
		n.Color = "yellow"
	}
	n.Folder = normalizeFolder(n.Folder)
	n.Tags = normalizeTags(n.Tags)
	if n.Desktop != nil {
		n.Desktop.normalize()
	}
}

func (n Note) Validate() error {
	if len([]rune(n.Title)) > MaxTitleRunes {
		return fmt.Errorf("标题不能超过 %d 字", MaxTitleRunes)
	}
	if len([]rune(n.Body)) > MaxBodyRunes {
		return fmt.Errorf("正文不能超过 %d 字", MaxBodyRunes)
	}
	if _, ok := allowedColors[n.Color]; !ok {
		return fmt.Errorf("未知颜色 %q", n.Color)
	}
	if err := validateFolder(n.Folder); err != nil {
		return err
	}
	if len(n.Tags) > MaxTags {
		return fmt.Errorf("标签不能超过 %d 个", MaxTags)
	}
	for _, tag := range n.Tags {
		if err := validateTag(tag); err != nil {
			return err
		}
	}
	return nil
}

// DisplayTitle returns an explicit title, the first non-empty body line, or a
// stable empty-note label. It never mutates the note.
func (n Note) DisplayTitle() string {
	if n.Title != "" {
		return n.Title
	}
	for _, line := range strings.Split(n.Body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		r := []rune(line)
		if len(r) > 32 {
			return string(r[:32]) + "…"
		}
		return line
	}
	return "未命名便笺"
}

func (n Note) DesktopVisible() bool {
	return !n.Archived && n.Desktop != nil && n.Desktop.Visible
}

// SearchBlob is title + body + folder + tags, used by keyword search.
func (n Note) SearchBlob() string {
	parts := make([]string, 0, 3+len(n.Tags))
	parts = append(parts, n.Title, n.Body, n.Folder)
	parts = append(parts, n.Tags...)
	return strings.Join(parts, "\n")
}

func (n Note) HasTag(want string) bool {
	want = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(want, "#")))
	if want == "" {
		return false
	}
	for _, tag := range n.Tags {
		if strings.ToLower(tag) == want {
			return true
		}
	}
	return false
}

func (n Note) InFolder(folder string) bool {
	folder = strings.TrimSpace(folder)
	if folder == "" || strings.EqualFold(folder, "all") {
		return true
	}
	if folder == InboxFolder {
		return n.Folder == ""
	}
	return strings.EqualFold(n.Folder, folder)
}

func normalizeFolder(s string) string {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, InboxFolder) {
		return ""
	}
	return s
}

func cleanTag(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, "#") {
		s = strings.TrimSpace(strings.TrimPrefix(s, "#"))
	}
	return s
}

func normalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, raw := range tags {
		tag := cleanTag(raw)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, tag)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func validateFolder(s string) error {
	if s == "" {
		return nil
	}
	if len([]rune(s)) > MaxFolderRunes {
		return fmt.Errorf("分类名不能超过 %d 字", MaxFolderRunes)
	}
	if strings.ContainsAny(s, "/\\") {
		return fmt.Errorf("分类名不能包含斜杠")
	}
	if strings.ContainsAny(s, "\n\r\t") {
		return fmt.Errorf("分类名不能包含换行或制表符")
	}
	return nil
}

func validateTag(s string) error {
	if s == "" {
		return fmt.Errorf("标签不能为空")
	}
	if len([]rune(s)) > MaxTagRunes {
		return fmt.Errorf("标签不能超过 %d 字", MaxTagRunes)
	}
	if strings.ContainsAny(s, ",#\n\r\t") {
		return fmt.Errorf("标签不能包含逗号、# 或空白控制符")
	}
	return nil
}

func DefaultDesktop() *DesktopLayout {
	return &DesktopLayout{Visible: true, XRatio: .72, YRatio: .18, Width: 340, Height: 280}
}

var (
	ErrNotFound     = errors.New("便笺不存在")
	ErrDesktopLimit = fmt.Errorf("桌面便笺最多同时显示 %d 张，请先从桌面收起一张", MaxDesktopVisible)
)

type ConflictError struct {
	Current Note
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("便笺已被其他窗口更新（当前版本 %d）", e.Current.Revision)
}

// Patch uses pointers to distinguish omitted fields from explicit zero values.
type Patch struct {
	BaseRevision uint64
	Title        *string
	Body         *string
	Color        *string
	Folder       *string
	Tags         *[]string
	Pinned       *bool
	Floating     *bool
	Archived     *bool
	Desktop      **DesktopLayout
}

type Filter struct {
	Query       string
	Tag         string
	Folder      string
	Archived    *bool
	Floating    *bool
	DesktopOnly bool
}

type Event struct {
	Kind string `json:"kind"`
	Note *Note  `json:"note,omitempty"`
	ID   string `json:"id,omitempty"`
}
