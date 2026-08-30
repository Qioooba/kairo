//go:build windows

package deskpet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"kairo/internal/winui"
)

// 桌面宠物（原生 Win32 实现）。
//
// 只依赖 Win32 分层窗口 + Go 自解 PNG 精灵图，CGO=0；主线目标为 Windows 10/11：
//   - 宠物窗口：WS_EX_LAYERED + UpdateLayeredWindow 逐帧贴透明 PNG；
//   - 面板窗口：普通 GDI 窗口，画名字/等级/经验条/皮肤网格，EDIT 控件改名；
//   - 数据：定时轮询 http://127.0.0.1:port/api/pet/state，差值触发经验漂浮。

const (
	petClass    = "KairoDeskPetClass_v1"
	panelClass  = "KairoDeskPetPanelClass_v1"
	bubbleClass = "KairoDeskPetBubbleClass_v1"

	// 宠物窗口尺寸：等级越高精灵画得越大，窗口固定容纳最大尺寸。
	petW       = 160
	petH       = 200
	spriteBase = 112 // Lv1 精灵基准尺寸
	spriteMax  = 152 // 高等级封顶尺寸（Level 封顶后不再变大）

	panelW = 300
	panelH = 504

	bubbleH      = 44
	bubblePad    = 14
	bubbleFontH  = 17
	bubbleShowMs = 3200

	defaultSkinID = "orange-cat"
)

var catCN = map[string]string{
	"cat": "猫系", "dog": "犬系", "rabbit": "兔系", "dragon": "龙系",
	"bird": "鸟系", "round": "圆滚系", "capybara": "水豚系", "fish": "鱼系",
	"fantasy": "幻想系", "other": "其他",
}

// -------- 主题色 --------
var (
	colPanelBg = rgb(30, 41, 59)
	colTitleBg = rgb(15, 23, 42)
	colText    = rgb(226, 232, 240)
	colDim     = rgb(148, 163, 184)
	colMute    = rgb(100, 116, 139)
	colAccent  = rgb(74, 111, 165)
	colBorder  = rgb(51, 65, 85)
	colWhite   = rgb(255, 255, 255)
)

// -------- 状态 JSON（/api/pet/state） --------
type posJSON struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type skinJSON struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Unlock   int    `json:"unlock"`
	Unlocked bool   `json:"unlocked"`
}

type stateJSON struct {
	Enabled     bool       `json:"enabled"`
	Name        string     `json:"name"`
	Owner       string     `json:"owner"`
	Level       int        `json:"level"`
	Exp         int64      `json:"exp"`
	Stage       string     `json:"stage"`
	Skin        string     `json:"skin"`
	Pos         posJSON    `json:"pos"`
	TotalEarned int64      `json:"total_earned"`
	NextExp     int64      `json:"next_exp"`
	TodayEarned int64      `json:"today_earned"`
	DailyCap    int64      `json:"daily_cap"`
	LastSync    string     `json:"last_sync"`
	Skins       []skinJSON `json:"skins"`
}

// -------- 运行态 --------
type animState struct {
	state string
	t0    int64 // UnixMilli
	dur   int64 // 限时状态时长 ms，0 = 常驻
}

type shakeSample struct {
	t  int64
	dx int32
}

type dragState struct {
	active  bool
	startX  int32
	startY  int32
	origX   int32
	origY   int32
	moved   int32
	lastX   int32
	samples []shakeSample
}

type expFloat struct {
	text string
	t0   int64
	dur  int64
	ox   int
}

type skinGroup struct {
	label string
	skins []skinJSON
}

type tileHit struct {
	id       string
	unlocked bool
	r        rect
}

type layeredWin struct {
	hdc  uintptr
	hbmp uintptr
	bits []byte
	w    int32
	h    int32
}

type deskpet struct {
	mu sync.Mutex

	baseURL    string
	loadSprite func(id string) ([]byte, error)
	dataDir    string
	host       *winui.Host
	running    bool

	hwndPet       uintptr
	hwndPanel     uintptr
	hwndEdit      uintptr
	hwndEditOwner uintptr
	hwndBubble    uintptr

	shown          bool
	posInitialized bool
	posX           int32
	posY           int32

	state     *stateJSON
	lastTotal int64
	lastLevel int
	lastStage string

	sprites map[string]*sprite
	loading map[string]bool
	failCnt map[string]int // 皮肤加载失败计数（≥3 次不再重试，防失败重试风暴）

	// panelVisible 面板当前是否显示。closePanel 只隐藏不销毁，EDIT 句柄会一直有效，
	// 轮询线程用此标记避免每秒向隐藏面板的输入框跨线程发同步消息。
	panelVisible bool

	anim     animState
	drag     *dragState
	floats   []expFloat
	floatSeq int

	panelScrollY int32

	// 话语气泡运行态
	bubbleTimerID uintptr
	bubbleText    string
	idleTalkTimer *time.Timer

	layered *layeredWin

	hFontTitle  uintptr
	hFontText   uintptr
	hFontSmall  uintptr
	hFontTiny   uintptr
	hFontBubble uintptr
}

var app = &deskpet{}

// spriteSem 皮肤异步加载并发信号量（上限 4）。
var spriteSem = make(chan struct{}, 4)

// -------- 对外 API --------

func Run(o Options) error {
	if o.BaseURL == "" {
		return errors.New("deskpet: BaseURL 不能为空")
	}
	if o.Host == nil {
		return errors.New("deskpet: Windows 原生 UI Host 不能为空")
	}
	app.mu.Lock()
	if app.running {
		app.mu.Unlock()
		return nil
	}
	app.baseURL = o.BaseURL
	app.loadSprite = o.LoadSprite
	app.dataDir = o.DataDir
	app.host = o.Host
	app.running = true
	if app.sprites == nil {
		app.sprites = map[string]*sprite{}
	}
	if app.loading == nil {
		app.loading = map[string]bool{}
	}
	if app.failCnt == nil {
		app.failCnt = map[string]int{}
	}
	app.mu.Unlock()

	if err := o.Host.Invoke(app.createWindows); err != nil {
		app.mu.Lock()
		app.running = false
		app.mu.Unlock()
		return err
	}
	go app.pollLoop()
	go app.renderLoop()
	return nil
}

func Toggle() {
	app.mu.Lock()
	hwnd := app.hwndPet
	running := app.running
	app.mu.Unlock()
	if !running || hwnd == 0 {
		return
	}
	postMessage(hwnd, wmAppToggle, 0, 0)
}

func IsShown() bool {
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.shown
}

func Shutdown() {
	app.mu.Lock()
	host := app.host
	running := app.running
	app.mu.Unlock()
	if !running || host == nil {
		return
	}
	_ = host.Invoke(func() error {
		app.closeAll()
		return nil
	})
}

// -------- 原生窗口（消息循环由 winui.Host 统一持有） --------

func (d *deskpet) createWindows() error {
	if err := registerClass(petClass, syscall.NewCallback(petWndProc)); err != nil {
		return fmt.Errorf("deskpet: 注册宠物窗口类: %w", err)
	}
	if err := registerClass(panelClass, syscall.NewCallback(panelWndProc)); err != nil {
		return fmt.Errorf("deskpet: 注册面板窗口类: %w", err)
	}
	if err := registerClass(bubbleClass, syscall.NewCallback(bubbleWndProc)); err != nil {
		return fmt.Errorf("deskpet: 注册气泡窗口类: %w", err)
	}

	hwnd := createWindowEx(wsExLayered|wsExToolwindow|wsExTopmost|wsExNoactivate, wsPopup, petClass, "KairoDeskPet", 0, 0, petW, petH, 0)
	if hwnd == 0 {
		return errors.New("deskpet: 创建宠物窗口失败")
	}
	d.mu.Lock()
	d.hwndPet = hwnd
	d.mu.Unlock()

	d.initPosition()

	// 创建话语气泡窗口（初始隐藏）。
	bubble := createWindowEx(wsExToolwindow|wsExTopmost|wsExNoactivate, wsPopup, bubbleClass, "KairoDeskPetBubble", 0, 0, 80, bubbleH, 0)
	d.mu.Lock()
	d.hwndBubble = bubble
	d.mu.Unlock()

	// 用户上次把宠物设为「显示」→ 本次启动自动显示。
	if d.loadPref() {
		d.mu.Lock()
		d.shown = true
		d.mu.Unlock()
		showWindow(hwnd, swShowNoActivate)
		d.showBubble("主人，我回来啦～")
		d.scheduleIdleTalk()
	}
	return nil
}

func (d *deskpet) initPosition() {
	d.mu.Lock()
	if !d.posInitialized {
		wa := workArea()
		d.posX = wa.Right - petW - 24
		d.posY = wa.Bottom - petH - 24
	}
	d.applyPosLocked()
	d.mu.Unlock()
}

func (d *deskpet) cleanupGDI() {
	if d.layered != nil {
		if d.layered.hdc != 0 {
			deleteDC(d.layered.hdc)
		}
		if d.layered.hbmp != 0 {
			deleteObject(d.layered.hbmp)
		}
		d.layered = nil
	}
	for _, h := range []uintptr{d.hFontTitle, d.hFontText, d.hFontSmall, d.hFontTiny} {
		if h != 0 {
			deleteObject(h)
		}
	}
}

// -------- 窗口过程 --------

func petWndProc(hwnd, msg, w, l uintptr) uintptr {
	switch msg {
	case wmAppToggle:
		app.handleToggle()
		return 0
	case wmAppQuit:
		app.closeAll()
		return 0
	case wmLButtonDown:
		app.onPetDown()
		return 0
	case wmMouseMove:
		app.onPetMove()
		return 0
	case wmLButtonUp:
		app.onPetUp()
		return 0
	case wmCaptureChange:
		app.onCaptureChange()
		return 0
	}
	return defWindowProc(hwnd, msg, w, l)
}

func panelWndProc(hwnd, msg, w, l uintptr) uintptr {
	switch msg {
	case wmPaint:
		app.paintPanel(hwnd)
		return 0
	case wmLButtonDown:
		app.onPanelDown(hwnd, l)
		return 0
	case wmMouseWheel:
		app.onPanelWheel(w)
		return 0
	case wmDestroy:
		app.mu.Lock()
		if app.hwndPanel == hwnd {
			app.hwndPanel = 0
			app.hwndEdit = 0
			app.hwndEditOwner = 0
			app.panelVisible = false
		}
		app.mu.Unlock()
		return 0
	}
	return defWindowProc(hwnd, msg, w, l)
}

func bubbleWndProc(hwnd, msg, w, l uintptr) uintptr {
	switch msg {
	case wmPaint:
		app.paintBubble(hwnd)
		return 0
	case wmTimer:
		if w == app.bubbleTimerID {
			app.hideBubble()
			return 0
		}
	}
	return defWindowProc(hwnd, msg, w, l)
}

// -------- 话语气泡 --------

// 话语池：按场景分组，随机取一句。
var talkGreeting = []string{
	"主人来啦～", "今天也要加油鸭！", "我是你的小伙伴，多多关照！",
	"工作再忙也要记得休息哦", "陪你上班，绝不摸鱼！", "又是元气满满的一天！",
}
var talkClick = []string{
	"嘿嘿，被你点到啦～", "主人找我什么事呀？", "揉揉我，我会变得更乖哦",
	"你说，我在听～", "要不要一起去冒险？", "摸头杀！好舒服～",
}
var talkExp = []string{
	"经验到手，继续冒险！", "又涨经验啦，真棒！", "慢慢变强中～",
	"冒险点数已入账！", "咕咕，经验值涨啦！", "离下个皮肤更近一步！",
}
var talkLevelUp = []string{
	"升级啦，我变更强了！", "我又变厉害啦！", "技能点+1，冲鸭！",
	"离大 BOSS 又近一步！", "新的皮肤在向我招手～",
}
var talkEvolve = []string{
	"我进化啦，快看我新样子！", "全新形态，闪亮登场！", "更帅了有没有～",
	"进化完成，战斗力爆表！",
}

// talkIdle 随机闲聊话术池（200 条）。含 {owner} 的会在显示时替换成主人名字。
var talkIdle = []string{
	// —— 问候 / 夸夸主人 ——
	"今天也元气满满呀", "主人今天真好看", "你一出现我就开心",
	"又是闪闪发光的一天", "见到你真高兴", "你笑起来真好看",
	"今天穿得真精神", "心情不错嘛", "感觉你今天状态很好",
	"你是我见过最棒的人", "和你一起真幸福", "嘿嘿，你来啦",
	"看到你就满血复活", "你的到来让我很开心", "天天见面也不腻",
	"你就是我的小太阳", "和你在一起最安心", "抱抱你呀",
	"你来了我就有精神了", "想你想得睡不着", "今天也要开开心心",
	"你最棒了，加油！", "有你在真好", "别累着自己",
	"你努力的样子真帅", "记得对自己好一点", "你值得拥有美好",
	"今天也在闪闪发光", "遇见你是我的幸运", "要一直陪着你",
	// —— 工作 / 加油 ——
	"工作加油哦", "今天的任务安排好了吗", "慢慢来，不着急",
	"专心的时候最帅", "累了就歇一歇", "该喝水啦",
	"记得吃午饭哦", "别忘了午休", "下午也要打起精神",
	"快下班啦，坚持住", "完成一件小事也是胜利", "别把自己绷太紧",
	"今天效率真高", "又搞定一件事，棒！", "慢慢做，稳一点",
	"困难只是暂时的", "一步一个脚印", "你已经做得很好了",
	"相信你自己", "努力不会被辜负", "失败是成功之母",
	"加油，我在陪着你", "你行的，冲！", "累了就摸摸鱼",
	"劳逸结合才高效", "偶尔放空也没关系", "对自己温柔一点",
	"再坚持一下下", "胜利就在前方", "好事多磨",
	// —— 卖萌 / 自嘲 ——
	"我在认真卖萌中", "看我圆圆的眼睛", "我是个快乐的小家伙",
	"蹦蹦跳跳最开心", "我可是很会卖萌的", "给我挠挠头嘛",
	"我也想被摸摸", "我可爱吗？可爱吧", "我这身板，一戳就倒",
	"咕噜咕噜，撒娇中", "我超乖的", "求表扬，求摸摸",
	"今天也是粘人小可爱", "我会乖乖等你的", "我的小短腿也能跑",
	"圆滚滚的才是正义", "我的尾巴会开花", "眼睛大大，烦恼小小",
	"我笑起来眼睛都没了", "我可是表情包本包", "小小一只，能量满格",
	// —— 关心主人 ——
	"你眼睛酸吗，歇会儿", "脖子累了吧，转转", "起来伸个懒腰",
	"喝口水补充能量", "晚饭记得按时吃", "少熬夜，早点睡",
	"冷了就多穿件衣服", "记得多运动呀", "长时间坐着对腰不好",
	"放松一下，看看窗外", "深呼吸，放轻松", "别低头看太久手机",
	"你健康我才开心", "照顾好自己最重要", "累了就闭上眼睛休息下",
	"饭要趁热吃哦", "天凉了注意保暖", "心情不好就跟我说",
	"有烦心事别憋着", "我会一直在这里", "加油，我会陪着你",
	"你开心我就开心", "想哭就哭出来", "抱抱你，一切都会好的",
	// —— 称呼主人（随机喊名字） ——
	"{owner}，今天也要加油鸭", "{owner}，你回来啦", "{owner}，我想你啦",
	"{owner}，记得喝水哦", "{owner}，别太累了", "{owner}，你最棒啦",
	"{owner}，陪我玩会儿嘛", "{owner}，该休息啦", "{owner}，摸摸头～",
	"{owner}，你好呀", "{owner}，今天真精神", "{owner}，我看好你",
	"{owner}，一起加油吧", "{owner}，辛苦了", "{owner}，晚上好呀",
	"{owner}，午安呀", "{owner}，早安哦", "{owner}，吃饭了吗",
	"{owner}，工作顺利吗", "{owner}，我在呢", "{owner}，笑一个嘛",
	"{owner}，你是最棒的", "{owner}，别逞强哦", "{owner}，听你安排",
	"{owner}，我们去冒险吧", "{owner}，摸摸我的头嘛", "{owner}，想听你说话",
	"{owner}，今天运气一定好", "{owner}，慢点也不怕", "{owner}，随叫随到",
	// —— 趣味 / 日常 ——
	"今天天气真不错", "我数到十，你就要笑", "猜猜我下一句说什么",
	"悄悄告诉你，我很喜欢你", "今天也是甜度满分", "我有一颗小心心送给你",
	"如果你不开心，我就唱歌", "我已经是个成熟的宠物了", "自己会换皮肤的那种",
	"我还会自己吃饭呢（假的）", "要不要给你讲个笑话", "从前有个宠物，它……它很可爱",
	"我是你的幸运物哦", "摸一摸，好运到", "每天抱一抱，烦恼全跑掉",
	"我的愿望是和你天天见面", "许个愿吧，我帮你实现", "今天也是被你宠的一天",
	"谁还不是个宝宝了", "我可盐可甜", "可咸可甜还可摆烂",
	"打工人打工魂，宠物也认真", "我也要努力升级", "升到满级带你飞",
	"我的金手指就是卖萌", "你看我多精神", "只要你不嫌弃，我就一直陪",
	"偶尔也要慢生活", "享受当下的小美好", "今天天气适合摸鱼",
	"喝杯茶，歇歇脚", "世界很大，先睡个觉", "人生苦短，及时行乐",
	"不着急，慢慢来", "顺其自然最好", "知足常乐嘛",
	"明天会更好的", "每一天都是新的开始", "晚安，做个好梦",
	"今天也是元气值满格的一天", "心里的小鹿又在撞墙了", "你猜我为什么这么快乐",
	"因为我遇见了你呀", "我的快乐就是这么简单", "看到你一切都好就放心了",
	"保持热爱，奔赴山海", "慢慢来，比较快", "办法总比困难多",
	"晴天适合见面，雨天适合睡觉", "我也想去吹吹风", "今天吃什么好呢",
	"好想喝奶茶呀", "健康最重要，其他都是小事",
	"小小的我也有一颗大大的心", "我是你的贴身小锦鲤", "好运正在向你靠近",
	"你认真的侧脸很好看", "今天也要做快乐的自己", "累了就靠在我身边吧",
	"世界很大，我们一起走", "你眼里有光，我心里有你", "再小的进步也值得庆祝",
	"深呼吸，把烦恼都呼出去", "笑一笑，十年少", "我的存在就是提醒你休息",
}

// ownerRepl 把话术里的 {owner} 替换为主人名字（未配置则用「主人」）。
func (d *deskpet) ownerRepl(text string) string {
	if !strings.Contains(text, "{owner}") {
		return text
	}
	name := "主人"
	d.mu.Lock()
	if d.state != nil && d.state.Owner != "" {
		name = d.state.Owner
	}
	d.mu.Unlock()
	return strings.ReplaceAll(text, "{owner}", name)
}

func randPick(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return list[time.Now().UnixNano()%int64(len(list))]
}

// showBubble 在宠物上方弹一句中文话语，3.2s 后自动隐藏。
// 线程安全：内部加锁，可被任何 goroutine 调用。
func (d *deskpet) showBubble(text string) {
	text = d.ownerRepl(text) // {owner} 占位统一替换成主人名字
	if text == "" {
		return
	}
	d.mu.Lock()
	hb := d.hwndBubble
	d.mu.Unlock()
	if hb == 0 {
		return
	}

	// 按字符数估算气泡宽度（中文按字号，ASCII 减半）。
	width := bubblePad*2 + bubbleFontH*len([]rune(text))
	if width < 70 {
		width = 70
	}

	d.mu.Lock()
	// 旧定时器先清理，避免文本未更新就被旧定时隐藏。
	if d.bubbleTimerID != 0 {
		killTimer(hb, d.bubbleTimerID)
	}
	d.bubbleText = text
	d.bubbleTimerID = 1
	px := d.posX
	py := d.posY
	d.mu.Unlock()

	// 气泡水平居中于宠物，垂直贴宠物顶。
	x := px + petW/2 - int32(width)/2
	y := py - bubbleH - 6
	wa := workArea()
	if x < wa.Left {
		x = wa.Left
	}
	if x+int32(width) > wa.Right {
		x = wa.Right - int32(width)
	}
	if y < wa.Top {
		y = py + petH + 6
	}

	setWindowPos(hb, 0, x, y, int32(width), bubbleH, swpNoZOrder|swpNoActivate)
	setWindowText(hb, text)
	invalidateRect(hb, true)
	showWindow(hb, swShowNoActivate)
	setTimer(hb, 1, bubbleShowMs)
}

// hideBubble 隐藏气泡并停用定时器。
func (d *deskpet) hideBubble() {
	d.mu.Lock()
	hb := d.hwndBubble
	id := d.bubbleTimerID
	d.bubbleTimerID = 0
	d.mu.Unlock()
	if hb == 0 {
		return
	}
	if id != 0 {
		killTimer(hb, id)
	}
	showWindow(hb, swHide)
}

// scheduleIdleTalk 安排一次随机闲聊（45~95s 后）。
func (d *deskpet) scheduleIdleTalk() {
	d.mu.Lock()
	if d.idleTalkTimer != nil {
		d.idleTalkTimer.Stop()
	}
	d.idleTalkTimer = time.AfterFunc(
		time.Duration(45+time.Now().UnixNano()%50)*time.Second,
		d.idleTalk,
	)
	d.mu.Unlock()
}

// idleTalk 随机闲聊（会用主人名字称呼），然后继续安排下一次。
func (d *deskpet) idleTalk() {
	d.mu.Lock()
	shown := d.shown
	d.mu.Unlock()
	if shown {
		d.showBubble(randPick(talkIdle))
	}
	d.scheduleIdleTalk()
}

// paintBubble 绘制话语气泡：白底圆角 + 深灰文本。
func (d *deskpet) paintBubble(hwnd uintptr) {
	hdc := getDC(hwnd)
	if hdc == 0 {
		return
	}
	defer releaseDC(hwnd, hdc)

	client := getClientRect(hwnd)
	if client.width() <= 0 || client.height() <= 0 {
		validateRect(hwnd)
		return
	}

	// 白底圆角 + 浅色描边（roundRect 用当前 pen 画边、当前 brush 填充）
	pen := createPen(1, rgb(148, 163, 184))
	bg := createSolidBrush(rgb(255, 255, 255))
	oldPen := selectObject(hdc, pen)
	oldBg := selectObject(hdc, bg)
	roundRect(hdc, &client, 12, 12)
	selectObject(hdc, oldBg)
	selectObject(hdc, oldPen)
	if bg != 0 {
		deleteObject(bg)
	}
	if pen != 0 {
		deleteObject(pen)
	}

	d.mu.Lock()
	text := d.bubbleText
	d.mu.Unlock()

	f := d.fontBubble()
	if f != 0 {
		oldFont := selectObject(hdc, f)
		setBkMode(hdc, transparent)
		setTextColor(hdc, rgb(30, 41, 59))
		tr := rect{6, 0, client.Right - 6, client.Bottom}
		textOutCentered(hdc, &tr, text)
		selectObject(hdc, oldFont)
	}

	validateRect(hwnd)
}

func (d *deskpet) fontBubble() uintptr {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.hFontBubble == 0 {
		d.hFontBubble = createFont(-bubbleFontH, false, "Microsoft YaHei")
	}
	return d.hFontBubble
}

// textOutCentered 在矩形内水平居中绘制文本（按字符数估算宽度）。
func textOutCentered(hdc uintptr, r *rect, s string) {
	if s == "" {
		return
	}
	w := int32(bubbleFontH * len([]rune(s)))
	x := r.Left + (r.Right-r.Left-w)/2
	if x < r.Left {
		x = r.Left
	}
	textOutW(hdc, x, (r.Top+r.Bottom-bubbleFontH)/2, s)
}

// -------- 显示/关闭 --------

func (d *deskpet) handleToggle() {
	d.mu.Lock()
	d.shown = !d.shown
	shown := d.shown
	hwnd := d.hwndPet
	d.mu.Unlock()
	if hwnd == 0 {
		return
	}
	if shown {
		showWindow(hwnd, swShowNoActivate)
		d.showBubble(randPick(talkGreeting))
		d.scheduleIdleTalk()
	} else {
		d.hideBubble()
		showWindow(hwnd, swHide)
	}
	d.savePref(shown)
}

func (d *deskpet) closeAll() {
	d.mu.Lock()
	p := d.hwndPet
	panel := d.hwndPanel
	bubble := d.hwndBubble
	if d.idleTalkTimer != nil {
		d.idleTalkTimer.Stop()
		d.idleTalkTimer = nil
	}
	d.hwndPet = 0
	d.hwndPanel = 0
	d.hwndEdit = 0
	d.hwndEditOwner = 0
	d.hwndBubble = 0
	d.running = false
	d.mu.Unlock()
	if panel != 0 {
		destroyWindow(panel)
	}
	if bubble != 0 {
		destroyWindow(bubble)
	}
	if p != 0 {
		destroyWindow(p)
	}
	d.cleanupGDI()
}

func (d *deskpet) closePanel() {
	d.mu.Lock()
	p := d.hwndPanel
	d.panelVisible = false
	d.mu.Unlock()
	if p != 0 {
		showWindow(p, swHide)
	}
}

// -------- 宠物拖拽/点击 --------

func (d *deskpet) onPetDown() {
	d.mu.Lock()
	hwnd := d.hwndPet
	d.mu.Unlock()
	if hwnd == 0 {
		return
	}
	setCapture(hwnd)
	p := getCursorPos()
	wr := getWindowRect(hwnd)
	d.mu.Lock()
	d.drag = &dragState{active: true, startX: p.X, startY: p.Y, origX: wr.Left, origY: wr.Top, lastX: p.X}
	d.setAnimLocked("drag", 0)
	d.mu.Unlock()
}

func (d *deskpet) onPetMove() {
	d.mu.Lock()
	drag := d.drag
	hwnd := d.hwndPet
	d.mu.Unlock()
	if drag == nil || !drag.active || hwnd == 0 {
		return
	}

	p := getCursorPos()
	wa := workArea()
	dx := p.X - drag.startX
	dy := p.Y - drag.startY
	moved := abs32(dx) + abs32(dy)
	if moved < drag.moved {
		moved = drag.moved
	}
	nx := clampI(drag.origX+dx, wa.Left, wa.Right-petW)
	ny := clampI(drag.origY+dy, wa.Top, wa.Bottom-petH)
	setWindowPos(hwnd, 0, nx, ny, 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)

	stepX := p.X - drag.lastX
	d.mu.Lock()
	drag.moved = moved
	drag.lastX = p.X
	d.posX = nx
	d.posY = ny
	if abs32(stepX) > 8 {
		drag.samples = append(drag.samples, shakeSample{t: time.Now().UnixMilli(), dx: stepX})
		d.detectShakeLocked()
	}
	d.mu.Unlock()
}

func (d *deskpet) onPetUp() {
	releaseCapture()
	d.mu.Lock()
	drag := d.drag
	d.mu.Unlock()
	if drag == nil {
		return
	}
	if drag.moved < 5 {
		d.mu.Lock()
		d.setAnimLocked("click", 500)
		d.mu.Unlock()
		d.openPanel()
		d.showBubble(randPick(talkClick))
	} else {
		d.snapToEdge()
		d.scheduleSavePos()
	}
	d.mu.Lock()
	d.drag = nil
	d.mu.Unlock()
}

func (d *deskpet) onCaptureChange() {
	d.mu.Lock()
	if d.drag != nil {
		d.drag.active = false
	}
	d.mu.Unlock()
}

func (d *deskpet) snapToEdge() {
	d.mu.Lock()
	hwnd := d.hwndPet
	x := d.posX
	y := d.posY
	d.mu.Unlock()
	if hwnd == 0 {
		return
	}
	wa := workArea()
	center := x + petW/2
	if center-wa.Left < 24 {
		x = wa.Left
	} else if wa.Right-center < 24 {
		x = wa.Right - petW
	}
	d.mu.Lock()
	d.posX = x
	d.mu.Unlock()
	setWindowPos(hwnd, 0, x, y, 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
}

func (d *deskpet) scheduleSavePos() {
	time.AfterFunc(500*time.Millisecond, func() { d.savePos() })
}

func (d *deskpet) savePos() {
	wa := workArea()
	d.mu.Lock()
	x := d.posX
	y := d.posY
	d.mu.Unlock()
	w := wa.Right - wa.Left
	h := wa.Bottom - wa.Top
	if w <= 0 || h <= 0 {
		return
	}
	fx := clampF(float64(x-wa.Left)/float64(w), 0, 1)
	fy := clampF(float64(y-wa.Top)/float64(h), 0, 1)
	_ = d.postJSON("/api/pet/pos", map[string]any{"x": fx, "y": fy})
}

func (d *deskpet) detectShakeLocked() {
	if d.drag == nil {
		return
	}
	now := time.Now().UnixMilli()
	win := d.drag.samples[:0]
	for _, s := range d.drag.samples {
		if now-s.t <= 1500 {
			win = append(win, s)
		}
	}
	d.drag.samples = win

	rev := 0
	for i := 1; i < len(win); i++ {
		if (win[i].dx > 0 && win[i-1].dx < 0) || (win[i].dx < 0 && win[i-1].dx > 0) {
			rev++
		}
	}
	if rev < 4 {
		return
	}
	d.drag.samples = nil

	ids := d.unlockedSkinIDsLocked()
	if len(ids) < 2 {
		return
	}
	cur := defaultSkinID
	if d.state != nil && d.state.Skin != "" {
		cur = d.state.Skin
	}
	idx := indexOfStr(ids, cur)
	next := ids[(idx+1+len(ids))%len(ids)]
	go d.applySkin(next)
}

// -------- 面板 --------

func (d *deskpet) openPanel() {
	d.mu.Lock()
	panel := d.hwndPanel
	hwndPet := d.hwndPet
	st := d.state
	d.mu.Unlock()
	if hwndPet == 0 {
		return
	}

	if panel == 0 {
		panel = createWindowEx(wsExToolwindow|wsExTopmost, wsPopup|wsBorder|wsClipChildren, panelClass, "KairoDeskPetPanel", 0, 0, panelW, panelH, 0)
		if panel == 0 {
			return
		}
		d.mu.Lock()
		d.hwndPanel = panel
		d.mu.Unlock()
		d.createEdit(panel)
		// 输入框预填当前名字与主人名。
		d.mu.Lock()
		e := d.hwndEdit
		eo := d.hwndEditOwner
		d.mu.Unlock()
		if e != 0 && st != nil && st.Name != "" {
			setWindowText(e, st.Name)
		}
		if eo != 0 && st != nil {
			setWindowText(eo, st.Owner)
		}
		if st != nil {
			// 皮肤全部走异步加载（每款一个 goroutine，完成即触发面板重绘）。
			// 不要在这里同步解码：108 款 PNG 全量解码在 UI 线程上要阻塞一秒多，
			// 会造成「点开宠物就卡死」。异步加载下首帧少量灰块会陆续刷成真图。
			for _, s := range st.Skins {
				d.spriteAsync(s.ID)
			}
		}
	}

	wr := getWindowRect(hwndPet)
	wa := workArea()
	px := wr.Left + petW/2 - panelW/2
	py := wr.Top - panelH - 8
	if px < wa.Left {
		px = wa.Left
	}
	if px+panelW > wa.Right {
		px = wa.Right - panelW
	}
	if py < wa.Top {
		py = wr.Bottom + 8
	}
	if py+panelH > wa.Bottom {
		py = wa.Bottom - panelH
	}

	setWindowPos(panel, 0, px, py, 0, 0, swpNoSize|swpNoZOrder)
	showWindow(panel, swShow)
	d.mu.Lock()
	d.panelVisible = true
	d.mu.Unlock()
	// 激活面板并聚焦，确保 WM_MOUSEWHEEL 送达（否则滚轮会落到浏览器窗口）。
	setForegroundWindow(panel)
	setFocus(panel)
	invalidateRect(panel, true)
}

func (d *deskpet) createEdit(panel uintptr) {
	// 统一用微软雅黑：EDIT 不设字体时默认旧系统字体，中文渲染/鼠标点选会偏移异常。
	hFont := d.font(13, false)

	// 宠物名字输入框
	edit := createWindowEx(0, wsChild|wsVisible|wsBorder|wsTabstop, "EDIT", "", 14, 128, 176, 24, panel)
	if edit == 0 {
		return
	}
	if hFont != 0 {
		sendMessage(edit, wmSetfont, hFont, 1)
	}
	d.mu.Lock()
	d.hwndEdit = edit
	d.mu.Unlock()

	// 主人的名字输入框
	owner := createWindowEx(0, wsChild|wsVisible|wsBorder|wsTabstop, "EDIT", "", 56, 156, 134, 24, panel)
	if owner == 0 {
		return
	}
	if hFont != 0 {
		sendMessage(owner, wmSetfont, hFont, 1)
	}
	d.mu.Lock()
	d.hwndEditOwner = owner
	d.mu.Unlock()
}

func (d *deskpet) onPanelDown(_ uintptr, l uintptr) {
	x := int32(int16(l & 0xffff))
	y := int32(int16((l >> 16) & 0xffff))

	// 关闭按钮
	if x >= panelW-32 && x <= panelW-8 && y >= 4 && y <= 28 {
		d.closePanel()
		return
	}
	// 确定（改名）
	if x >= 200 && x <= 286 && y >= 128 && y <= 152 {
		d.doRename()
		return
	}
	// 确定（主人名字）
	if x >= 200 && x <= 286 && y >= 156 && y <= 180 {
		d.doSaveOwner()
		return
	}

	d.mu.Lock()
	st := d.state
	scroll := d.panelScrollY
	d.mu.Unlock()
	if st == nil || !st.Enabled {
		return
	}
	_, _, tiles := computeGrid(st.Skins)
	gridTop := int32(190)
	curID := st.Skin
	for _, t := range tiles {
		if !t.unlocked {
			continue
		}
		sy := t.r.Top - scroll
		dr := rect{t.r.Left, gridTop + sy, t.r.Right, gridTop + sy + t.r.height()}
		if x >= dr.Left && x < dr.Right && y >= dr.Top && y < dr.Bottom {
			if t.id != curID {
				go d.applySkin(t.id)
			}
			return
		}
	}
}

func (d *deskpet) onPanelWheel(w uintptr) {
	delta := int16((w >> 16) & 0xffff)
	d.mu.Lock()
	d.panelScrollY -= int32(delta) / 120 * 30
	d.mu.Unlock()
	d.repaintPanel()
}

func (d *deskpet) doRename() {
	d.mu.Lock()
	edit := d.hwndEdit
	d.mu.Unlock()
	if edit == 0 {
		return
	}
	name := strings.TrimSpace(getWindowText(edit))
	if name == "" {
		return
	}
	// 成功后不清空：框里就是用户刚输入的新名字，清空反而让面板显示空框。
	_ = d.postJSON("/api/pet/name", map[string]any{"name": name})
	d.pollOnce()
}

func (d *deskpet) doSaveOwner() {
	d.mu.Lock()
	edit := d.hwndEditOwner
	d.mu.Unlock()
	if edit == 0 {
		return
	}
	name := strings.TrimSpace(getWindowText(edit))
	if name == "" {
		return
	}
	_ = d.postJSON("/api/pet/owner", map[string]any{"name": name})
	d.pollOnce()
}

func (d *deskpet) applySkin(id string) {
	if err := d.postJSON("/api/pet/skin", map[string]any{"skin": id}); err == nil {
		d.mu.Lock()
		d.setAnimLocked("skin", 650)
		d.mu.Unlock()
	}
	d.pollOnce()
}

func (d *deskpet) repaintPanel() {
	d.mu.Lock()
	p := d.hwndPanel
	d.mu.Unlock()
	if p != 0 {
		invalidateRect(p, true)
	}
}

// -------- 面板绘制 --------

func (d *deskpet) font(h int32, bold bool) uintptr {
	switch {
	case bold && h == 15:
		if d.hFontTitle == 0 {
			d.hFontTitle = createFont(-h, true, "Microsoft YaHei")
		}
		return d.hFontTitle
	case !bold && h == 13:
		if d.hFontText == 0 {
			d.hFontText = createFont(-h, false, "Microsoft YaHei")
		}
		return d.hFontText
	case !bold && h == 11:
		if d.hFontSmall == 0 {
			d.hFontSmall = createFont(-h, false, "Microsoft YaHei")
		}
		return d.hFontSmall
	case !bold && h == 10:
		if d.hFontTiny == 0 {
			d.hFontTiny = createFont(-h, false, "Microsoft YaHei")
		}
		return d.hFontTiny
	}
	return createFont(-h, bold, "Microsoft YaHei")
}

func (d *deskpet) paintPanel(hwnd uintptr) {
	d.mu.Lock()
	st := d.state
	scroll := d.panelScrollY
	d.mu.Unlock()

	hdc := getDC(hwnd)
	if hdc == 0 {
		return
	}
	defer releaseDC(hwnd, hdc)

	client := getClientRect(hwnd)
	if client.width() <= 0 || client.height() <= 0 {
		client = rect{0, 0, panelW, panelH}
	}

	if b := createSolidBrush(colPanelBg); b != 0 {
		fillRect(hdc, &client, b)
		deleteObject(b)
	}
	titleRect := rect{0, 0, client.Right, 30}
	if b := createSolidBrush(colTitleBg); b != 0 {
		fillRect(hdc, &titleRect, b)
		deleteObject(b)
	}

	setBkMode(hdc, transparent)

	fTitle := d.font(15, true)
	fText := d.font(13, false)
	fSmall := d.font(11, false)
	fTiny := d.font(10, false)

	selectObject(hdc, fTitle)
	setTextColor(hdc, colText)
	textOutW(hdc, 12, 7, "宠物")
	setTextColor(hdc, colDim)
	textOutW(hdc, panelW-28, 7, "×")

	name := "小K"
	lvl := 1
	stage := "蛋"
	var exp, nextExp, today, dailyCap, total int64
	enabled := true
	if st != nil {
		if st.Name != "" {
			name = st.Name
		}
		if st.Level >= 1 {
			lvl = st.Level
		}
		stage = stageCN(st.Stage)
		exp = st.Exp
		nextExp = st.NextExp
		today = st.TodayEarned
		dailyCap = st.DailyCap
		total = st.TotalEarned
		enabled = st.Enabled
	}

	selectObject(hdc, fText)
	setTextColor(hdc, colText)
	textOutW(hdc, 14, 40, fmt.Sprintf("%s  Lv %d  ·  %s", name, lvl, stage))

	// 经验条
	barBg := rect{14, 62, client.Right - 14, 70}
	if b := createSolidBrush(colTitleBg); b != 0 {
		fillRect(hdc, &barBg, b)
		deleteObject(b)
	}
	if nextExp > 0 {
		pct := clampF(float64(exp)*100/float64(nextExp), 0, 100)
		fillW := int32(float64(barBg.width()) * pct / 100)
		if fillW > 0 {
			if fillW < 2 {
				fillW = 2
			}
			fill := rect{barBg.Left, barBg.Top, barBg.Left + fillW, barBg.Bottom}
			if b := createSolidBrush(colAccent); b != 0 {
				fillRect(hdc, &fill, b)
				deleteObject(b)
			}
		}
	}

	selectObject(hdc, fSmall)
	setTextColor(hdc, colDim)
	textOutW(hdc, 14, 76, fmt.Sprintf("经验 %d / %d", exp, nextExp))
	textOutW(hdc, 14, 96, fmt.Sprintf("今日经验 %d / %d", today, dailyCap))
	textOutW(hdc, 14, 112, fmt.Sprintf("总经验 %d", total))

	// 改名按钮
	btnRect := rect{200, 128, 286, 152}
	if b := createSolidBrush(colAccent); b != 0 {
		fillRect(hdc, &btnRect, b)
		deleteObject(b)
	}
	selectObject(hdc, fSmall)
	setTextColor(hdc, colWhite)
	textOutW(hdc, 224, 132, "确定")

	// 主人名字行（输入框在 56,156 起，对应 createEdit 的 owner EDIT）
	selectObject(hdc, fSmall)
	setTextColor(hdc, colDim)
	textOutW(hdc, 14, 161, "主人")
	ownerBtn := rect{200, 156, 286, 180}
	if b := createSolidBrush(colAccent); b != 0 {
		fillRect(hdc, &ownerBtn, b)
		deleteObject(b)
	}
	selectObject(hdc, fSmall)
	setTextColor(hdc, colWhite)
	textOutW(hdc, 224, 160, "保存")

	// 皮肤标题
	selectObject(hdc, fSmall)
	setTextColor(hdc, colDim)
	textOutW(hdc, 14, 186, "皮肤（点击穿戴，摇晃宠物快速换肤）")

	gridTop := int32(196)
	viewH := client.Bottom - 8 - gridTop
	if viewH <= 0 {
		viewH = 1
	}

	if !enabled {
		selectObject(hdc, fText)
		setTextColor(hdc, colDim)
		textOutW(hdc, 14, gridTop+8, "宠物未开启：请在「关于」页连点 6 次解锁")
		validateRect(hwnd)
		return
	}

	skins := []skinJSON{}
	if st != nil {
		skins = st.Skins
	}
	_, contentH, tiles := computeGrid(skins)
	maxScroll := contentH - viewH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if scroll < 0 {
		scroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}
	d.mu.Lock()
	d.panelScrollY = scroll
	d.mu.Unlock()

	curID := defaultSkinID
	if st != nil && st.Skin != "" {
		curID = st.Skin
	}

	unlockOf := map[string]int{}
	for _, s := range skins {
		unlockOf[s.ID] = s.Unlock
	}

	for _, t := range tiles {
		sy := t.r.Top - scroll
		dr := rect{t.r.Left, gridTop + sy, t.r.Right, gridTop + sy + t.r.height()}
		if dr.Bottom < gridTop || dr.Top > client.Bottom-8 {
			continue
		}
		if b := createSolidBrush(colTitleBg); b != 0 {
			fillRect(hdc, &dr, b)
			deleteObject(b)
		}

		sp := d.spriteAsync(t.id)
		if sp != nil {
			bits := compositeOnBG(sp, 0, 15, 23, 42, int(dr.width()), int(dr.height()))
			if !t.unlocked {
				grayDim(bits, 0.45)
			}
			stretchDIBits(hdc, dr.Left, dr.Top, dr.width(), dr.height(), unsafe.Pointer(&bits[0]), dr.width(), dr.height())
		} else {
			if b := createSolidBrush(colBorder); b != 0 {
				fillRect(hdc, &dr, b)
				deleteObject(b)
			}
		}

		borderCol := colBorder
		if t.id == curID {
			borderCol = colAccent
		}
		drawBorder(hdc, dr, 2, borderCol)

		if !t.unlocked {
			selectObject(hdc, fTiny)
			setTextColor(hdc, colWhite)
			textOutW(hdc, dr.Left+2, dr.Bottom-14, fmt.Sprintf("Lv%d", unlockOf[t.id]))
		}
	}

	validateRect(hwnd)
}

func drawBorder(hdc uintptr, r rect, t int32, color uint32) {
	b := createSolidBrush(color)
	if b == 0 {
		return
	}
	defer deleteObject(b)
	if t <= 0 {
		t = 1
	}
	fillRect(hdc, &rect{r.Left, r.Top, r.Right, r.Top + t}, b)
	fillRect(hdc, &rect{r.Left, r.Bottom - t, r.Right, r.Bottom}, b)
	fillRect(hdc, &rect{r.Left, r.Top, r.Left + t, r.Bottom}, b)
	fillRect(hdc, &rect{r.Right - t, r.Top, r.Right, r.Bottom}, b)
}

func computeGrid(skins []skinJSON) (groups []skinGroup, contentH int32, tiles []tileHit) {
	const (
		margin   = 14
		tileSize = 40
		gap      = 6
		cols     = 6
	)

	index := map[string]int{}
	for _, s := range skins {
		cat := s.Category
		if cat == "" {
			cat = "other"
		}
		idx, ok := index[cat]
		if !ok {
			idx = len(groups)
			index[cat] = idx
			groups = append(groups, skinGroup{label: catLabel(cat)})
		}
		groups[idx].skins = append(groups[idx].skins, s)
	}

	y := int32(0)
	for _, g := range groups {
		y += 22 // 分类标题行
		for i, s := range g.skins {
			col := i % cols
			row := i / cols
			tx := int32(margin) + int32(col)*(tileSize+gap)
			ty := y + int32(row)*(tileSize+gap)
			tiles = append(tiles, tileHit{id: s.ID, unlocked: s.Unlocked, r: rect{tx, ty, tx + tileSize, ty + tileSize}})
		}
		rows := (len(g.skins) + cols - 1) / cols
		y += int32(rows)*(tileSize+gap) - gap + 6
	}
	contentH = y
	return groups, contentH, tiles
}

func catLabel(cat string) string {
	if s, ok := catCN[cat]; ok {
		return s
	}
	return cat
}

// -------- 宠物绘制 --------

func (d *deskpet) renderLoop() {
	t := time.NewTicker(66 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		d.renderPet()
	}
}

func (d *deskpet) renderPet() {
	d.mu.Lock()
	if !d.shown || d.hwndPet == 0 {
		d.mu.Unlock()
		return
	}
	st := d.state
	anim := d.anim
	floats := make([]expFloat, len(d.floats))
	copy(floats, d.floats)
	hwnd := d.hwndPet
	d.mu.Unlock()

	skinID := defaultSkinID
	if st != nil && st.Skin != "" {
		skinID = st.Skin
	}
	sp := d.spriteBlocking(skinID)

	c := newCanvas(petW, petH)
	d.drawPetFrame(c, sp, st, anim, floats)
	d.present(hwnd, c)

	now := time.Now().UnixMilli()
	d.mu.Lock()
	if anim.dur > 0 && now-anim.t0 >= anim.dur && d.anim.t0 == anim.t0 && d.anim.state == anim.state {
		d.anim = animState{state: "idle", t0: now}
	}
	alive := d.floats[:0]
	for _, f := range d.floats {
		if now-f.t0 < f.dur {
			alive = append(alive, f)
		}
	}
	d.floats = alive
	d.mu.Unlock()
}

// spriteSize 等级越高精灵画得越大（每级 +1px，封顶 spriteMax）。
func spriteSize(level int) int {
	s := spriteBase + (level - 1)
	if s < spriteBase {
		s = spriteBase
	}
	if s > spriteMax {
		s = spriteMax
	}
	return s
}

func (d *deskpet) drawPetFrame(c *canvas, sp *sprite, st *stateJSON, anim animState, floats []expFloat) {
	now := time.Now().UnixMilli()
	frame, dy := d.animFrame(now, anim, sp)

	lvl := 1
	if st != nil && st.Level >= 1 {
		lvl = st.Level
	}
	size := spriteSize(lvl)
	dx := (petW - size) / 2
	dy0 := petH - size - 4

	// 高等级（Lv16+）金色光环，画在精灵下层。
	if sp != nil && lvl >= 16 {
		c.drawAura(petW/2, dy0+size/2, size/2+2)
	}

	if sp != nil {
		c.blitSprite(sp, frame, dx, dy0+dy, size, size)
	} else {
		c.fillRoundRect(dx, dy0, size, size, 14, 0xAA, 0xA4, 0x9A)
	}

	d.drawBadge(c, lvl)
	for i, f := range floats {
		d.drawFloat(c, f, i, now)
	}
}

func (d *deskpet) animFrame(now int64, a animState, sp *sprite) (int, int) {
	frames := 4
	fps := float64(6)
	if sp != nil && sp.h > 0 {
		frames = sp.w / sp.h
		if frames < 1 {
			frames = 1
		}
	}
	sec := float64(now-a.t0) / 1000.0
	frame := int(sec*fps) % frames
	dy := 0
	switch a.state {
	case "drag":
		frame = int(sec*fps*2) % frames
	case "click":
		seq := []int{1, 3, 1, 3}
		p := int(sec / 0.125)
		if p > len(seq)-1 {
			p = len(seq) - 1
		}
		frame = seq[p] % frames
	case "levelup":
		frame = int(sec*fps*1.5) % frames
		dy = -int(math.Abs(math.Sin(sec*math.Pi*2)) * 16)
	case "evolve":
		frame = int(sec*fps) % frames
		dy = -int(math.Abs(math.Sin(sec*math.Pi)) * 6)
	case "skin":
		frame = int(sec*fps) % frames
	}
	return frame, dy
}

func (d *deskpet) drawBadge(c *canvas, lvl int) {
	text := fmt.Sprintf("Lv%d", lvl)
	scale := 2
	w := textW(text, scale)
	x := petW - w - 8
	y := 4
	c.fillRoundRect(x-4, y-1, w+8, 16, 7, 165, 111, 74)
	c.drawText(text, x, y, scale, 255, 255, 255, false)
}

func (d *deskpet) drawFloat(c *canvas, f expFloat, seq int, now int64) {
	p := float64(now-f.t0) / float64(f.dur)
	if p >= 1 {
		return
	}
	const scale = 4
	dy := -int(p * 80)
	x := (petW-textW(f.text, scale))/2 + f.ox
	y := 22 + seq*36 + dy
	c.drawText(f.text, x, y, scale, 122, 226, 255, true)
}

func (d *deskpet) presenter(w, h int32) *layeredWin {
	d.mu.Lock()
	p := d.layered
	d.mu.Unlock()
	if p != nil && p.w == w && p.h == h {
		return p
	}
	hdc := createCompatibleDC(0)
	if hdc == 0 {
		return nil
	}
	hbmp, bits := createDIBSection(hdc, w, h)
	if hbmp == 0 {
		deleteDC(hdc)
		return nil
	}
	selectObject(hdc, hbmp)
	p = &layeredWin{hdc: hdc, hbmp: hbmp, bits: unsafe.Slice((*byte)(bits), int(w*h*4)), w: w, h: h}
	d.mu.Lock()
	d.layered = p
	d.mu.Unlock()
	return p
}

func (d *deskpet) present(hwnd uintptr, c *canvas) {
	p := d.presenter(int32(c.w), int32(c.h))
	if p == nil {
		return
	}
	c.premultiplyInto(p.bits)
	bf := blendFunction{BlendOp: acSrcOver, SourceConstantAlpha: 255, AlphaFormat: acSrcAlpha}
	updateLayeredWindow(hwnd, p.hdc, int32(c.w), int32(c.h), nil, 0, &bf)
}

// -------- 轮询 / 网络 --------

func (d *deskpet) pollLoop() {
	d.pollOnce()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		d.pollOnce()
	}
}

func (d *deskpet) pollOnce() {
	st, err := fetchState(d.baseURL)
	if err != nil {
		return
	}

	// 线程规则：GetWindowText / SetWindowText / SetWindowPos 对别的线程创建的窗口
	// 是同步调用（等对方消息循环处理）。绝不能在持有 d.mu 时调用——否则消息线程
	// 若恰好在 paintPanel 里等 d.mu，两边互相等死：宠物卡死、悬停转圈。
	// 这里锁内只读写内存字段，所有窗口操作拿到锁外执行。
	var (
		bubble     string
		posApply   bool
		posX, posY int32
		editName   uintptr
		editOwner  uintptr
	)
	d.mu.Lock()
	prev := d.state
	d.state = st
	// 面板是否值得重绘：任何展示字段变化才重绘，避免 1s 一次无意义全量重绘。
	changed := prev == nil ||
		prev.Name != st.Name || prev.Owner != st.Owner ||
		prev.Level != st.Level || prev.Exp != st.Exp ||
		prev.NextExp != st.NextExp || prev.TodayEarned != st.TodayEarned ||
		prev.DailyCap != st.DailyCap || prev.Skin != st.Skin || prev.Stage != st.Stage
	if prev != nil && prev.Enabled {
		if st.TotalEarned > d.lastTotal {
			d.spawnFloatLocked(st.TotalEarned - d.lastTotal)
			bubble = randPick(talkExp)
		}
		if d.lastLevel > 0 && st.Level > d.lastLevel {
			d.setAnimLocked("levelup", 1000)
			bubble = randPick(talkLevelUp)
		}
		if d.lastStage != "" && st.Stage != "" && st.Stage != d.lastStage {
			d.setAnimLocked("evolve", 900)
			bubble = randPick(talkEvolve)
		}
	}
	d.lastTotal = st.TotalEarned
	d.lastLevel = st.Level
	d.lastStage = st.Stage
	if !d.posInitialized {
		d.posInitialized = true
		// 只在锁内算坐标，窗口移动放锁外（setWindowPos 是跨线程同步调用）。
		wa := workArea()
		if w := wa.Right - wa.Left; w > 0 && wa.Bottom-wa.Top > 0 {
			x := clampF(st.Pos.X, 0, 1)
			y := clampF(st.Pos.Y, 0, 1)
			posX = wa.Left + int32(x*float64(w-petW))
			posY = wa.Top + int32(y*float64(wa.Bottom-wa.Top-petH))
			d.posX = posX
			d.posY = posY
			posApply = true
		}
	}
	// 输入框同步只在面板可见时做（面板隐藏后 EDIT 仍在，别再每秒跨线程发消息）。
	if d.panelVisible {
		editName = d.hwndEdit
		editOwner = d.hwndEditOwner
	}
	d.mu.Unlock()

	// —— 以下窗口操作均不持有 d.mu ——

	if posApply {
		d.mu.Lock()
		hwnd := d.hwndPet
		d.mu.Unlock()
		if hwnd != 0 {
			setWindowPos(hwnd, 0, posX, posY, 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
		}
	}

	// 同步改名输入框：仅当服务端名字真的变了（本面板点确定后的回显、网页端改名）
	// 且用户没有正在编辑（框内仍是上一轮的旧名字）时才回写。
	// 注意：内容相同也绝不 SetWindowText——EDIT 收到 WM_SETTEXT 会把光标重置回行首，
	// 用户点进输入框把光标挪到末尾，一秒后就会被拽回开头。
	var syncName, syncOwner bool
	if editName != 0 {
		cur := getWindowText(editName)
		if prev != nil && prev.Name != st.Name && cur == prev.Name && st.Name != "" {
			syncName = true
		}
	}
	// 同步主人名输入框（逻辑同上；空串回写没有意义，跳过）。
	if editOwner != 0 {
		cur := getWindowText(editOwner)
		if prev != nil && prev.Owner != st.Owner && cur == prev.Owner && st.Owner != "" {
			syncOwner = true
		}
	}
	if syncName || syncOwner {
		// 二次确认面板仍开着：上面两次跨线程调用期间面板可能已被关掉。
		d.mu.Lock()
		visible := d.panelVisible
		d.mu.Unlock()
		if visible {
			if syncName {
				setWindowText(editName, st.Name)
			}
			if syncOwner {
				setWindowText(editOwner, st.Owner)
			}
		}
	}

	if bubble != "" {
		d.showBubble(bubble)
	}
	if changed {
		d.repaintPanel()
	}
}

func (d *deskpet) spawnFloatLocked(n int64) {
	if n <= 0 {
		return
	}
	if len(d.floats) >= 5 {
		d.floats = d.floats[1:]
	}
	ox := (d.floatSeq%5 - 2) * 7
	d.floatSeq++
	d.floats = append(d.floats, expFloat{
		text: fmt.Sprintf("+%d", n),
		t0:   time.Now().UnixMilli(),
		dur:  1800,
		ox:   ox,
	})
}

func (d *deskpet) setAnimLocked(state string, dur int64) {
	d.anim = animState{state: state, t0: time.Now().UnixMilli(), dur: dur}
}

func (d *deskpet) applyPosLocked() {
	if d.hwndPet != 0 {
		setWindowPos(d.hwndPet, 0, d.posX, d.posY, 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
	}
}

func (d *deskpet) unlockedSkinIDsLocked() []string {
	var ids []string
	if d.state == nil {
		return ids
	}
	for _, s := range d.state.Skins {
		if s.Unlocked {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

func (d *deskpet) spriteBlocking(id string) *sprite {
	d.mu.Lock()
	sp := d.sprites[id]
	failed := d.failCnt[id] >= 3
	d.mu.Unlock()
	if sp != nil || failed {
		// 已加载直接用；连续失败 3 次直接放弃，避免渲染循环每 66ms
		// 反复 fetch+decode 同一张坏图把 CPU 打满。
		return sp
	}
	data, err := fetchSpriteBytes(d.baseURL, id)
	if err != nil {
		d.noteSpriteFail(id)
		return nil
	}
	sp, err = decodeSprite(data)
	if err != nil {
		d.noteSpriteFail(id)
		return nil
	}
	d.mu.Lock()
	d.sprites[id] = sp
	delete(d.failCnt, id)
	d.mu.Unlock()
	return sp
}

func (d *deskpet) noteSpriteFail(id string) {
	d.mu.Lock()
	d.failCnt[id]++
	d.mu.Unlock()
}

// panelDebounce 面板重绘防抖状态（全局单例，app 唯一）。
var panelDebounce struct {
	mu    sync.Mutex
	timer *time.Timer
}

// schedulePanelRepaint 防抖触发面板重绘。
// 面板打开会异步加载 108 款皮肤，每款完成都 repaintPanel 的话消息线程会被
// WM_PAINT 洪水占满（表现为面板开出来那几秒整个宠物卡顿/假死）。
// 合并成每 120ms 至多一次，加载完成后一次性把新到的皮肤刷上去。
func (d *deskpet) schedulePanelRepaint() {
	panelDebounce.mu.Lock()
	defer panelDebounce.mu.Unlock()
	if panelDebounce.timer != nil {
		return // 已有待触发的重绘，本次合并
	}
	panelDebounce.timer = time.AfterFunc(120*time.Millisecond, func() {
		panelDebounce.mu.Lock()
		panelDebounce.timer = nil
		panelDebounce.mu.Unlock()
		d.repaintPanel()
	})
}

func (d *deskpet) spriteAsync(id string) *sprite {
	d.mu.Lock()
	sp := d.sprites[id]
	if sp != nil {
		d.mu.Unlock()
		return sp
	}
	if d.loading[id] || d.failCnt[id] >= 3 {
		// 正在加载 / 已失败放弃：直接返回 nil（画灰块占位），不再起 goroutine。
		d.mu.Unlock()
		return nil
	}
	d.loading[id] = true
	d.mu.Unlock()

	go func() {
		// 并发限 4：108 款皮肤不至于同时打爆本地 HTTP / 占满 CPU。
		spriteSem <- struct{}{}
		defer func() { <-spriteSem }()

		ok := false
		if data, err := fetchSpriteBytes(d.baseURL, id); err == nil {
			if sp2, err2 := decodeSprite(data); err2 == nil {
				d.mu.Lock()
				d.sprites[id] = sp2
				delete(d.failCnt, id)
				d.mu.Unlock()
				ok = true
			}
		}
		if !ok {
			// 失败计数（≥3 次后 paintPanel 不再为它起加载协程）。
			// 若不计数，会出现「加载失败 → 完成回调重绘 → 又起加载 → 又失败」
			// 的无限重试风暴，把消息线程活活拖死。
			d.noteSpriteFail(id)
		}
		d.mu.Lock()
		delete(d.loading, id)
		d.mu.Unlock()
		if ok {
			d.schedulePanelRepaint()
		}
	}()
	return nil
}

func (d *deskpet) postJSON(path string, payload map[string]any) error {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", d.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func fetchState(baseURL string) (*stateJSON, error) {
	resp, err := httpGet(baseURL + "/api/pet/state")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("state HTTP %d", resp.StatusCode)
	}
	var st stateJSON
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&st); err != nil {
		return nil, err
	}
	return &st, nil
}

func fetchSpriteBytes(baseURL, id string) ([]byte, error) {
	if app.loadSprite != nil {
		return app.loadSprite(id)
	}
	resp, err := httpGet(baseURL + "/static/img/pet/skins/" + id + ".png")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sprite HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func httpGet(url string) (*http.Response, error) {
	cl := &http.Client{Timeout: 3 * time.Second}
	return cl.Get(url)
}

// -------- 显示偏好持久化 --------

type petPref struct {
	Shown bool `json:"shown"`
}

func (d *deskpet) prefPath() string {
	if d.dataDir == "" {
		return ""
	}
	return filepath.Join(d.dataDir, "deskpet.json")
}

func (d *deskpet) loadPref() bool {
	p := d.prefPath()
	if p == "" {
		return false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	var pref petPref
	if json.Unmarshal(b, &pref) != nil {
		return false
	}
	return pref.Shown
}

func (d *deskpet) savePref(shown bool) {
	p := d.prefPath()
	if p == "" {
		return
	}
	b, _ := json.Marshal(petPref{Shown: shown})
	_ = os.WriteFile(p, b, 0o644)
}

// -------- 小工具 --------

func stageCN(s string) string {
	switch s {
	case "egg":
		return "蛋"
	case "hatchling":
		return "幼体"
	case "grown":
		return "成体"
	case "mythic":
		return "神话"
	}
	if s == "" {
		return "蛋"
	}
	return s
}

func clampI(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

func indexOfStr(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}
