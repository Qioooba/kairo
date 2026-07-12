// Package sponsor 提供 Kairo 投喂作者排行榜的服务端能力 (v0.14 起)。
//
// 整体设计 (跟用户拍板过的):
//   - 数据源: 内部 Java 后端 (通过 endpointclient 走主备切换), URL 形态同
//     /credit/httpInterface?channelID=PC&serviceID=KairoSponsorLeaderboardAction
//   - serviceID 是 Java 端反射路由的 key, 写死在源码, 不让 config 覆盖 (跟
//     KairoActivateAction 同款)
//   - 调 Java 端拿前 50 名 (排名 + 真实姓名 + 3 列杯数 + 总杯数 + 日期)
//   - 前端 50 个昵称 (武林神话/一代武神/...) 写死, 按 rank 1:1 拼到真实姓名前面
//   - 失败不兜底: 网络错 / Java 端拒绝 → handler 返回 502/200, 前端显示错误让用户重试
//
// 不防什么:
//   - 反编译 Go 端拿 Auth 串 (跟 license 同款, 用户接受这个风险)
//   - Java 端注入风险 (用 ? 拼接但参数值 URL-encode 过, 不会逃逸出 query)
package sponsor

import "time"

// Entry 排行榜里的一行 (一个真实姓名 + 3 列杯数 + 总杯数 + 日期)。
//
// 字段命名按用户原话: 库迪=cotti, 瑞幸=lucky, 奶茶=milktea (用户原话:
// "瑞幸是LUCKY" + "random 就是奶茶啊 随机奶茶数量")。
//
// JSON 字段名用 snake_case (小写), 跟 Java 端 JSONObject.toJSONString 默认
// 风格一致, 也跟前端 brand id (cotti / luckin / milktea) 对齐。
type Entry struct {
	Rank      int       `json:"rank"`       // 1-based 排名, Java 端 ROW_NUMBER() OVER (ORDER BY TOTAL DESC)
	RealName  string    `json:"real_name"`  // 真实姓名 (前端拼到 NICKNAMES[rank-1] 后面)
	Cotti     int       `json:"cotti"`      // 库迪杯数
	Lucky     int       `json:"lucky"`      // 瑞幸杯数
	Milktea   int       `json:"milktea"`    // 奶茶杯数 (用户原话: "random 就是奶茶啊 随机奶茶数量")
	Total     int       `json:"total"`      // 总杯数 = cotti+lucky+milktea (Java 端冗余存, 方便 ORDER BY)
	Date      string    `json:"date"`       // YYYY-MM-DD, 首次赞助日期 (前端显示用)
	UpdatedAt time.Time `json:"updated_at"` // Java 端最后更新时间
}

// LeaderboardResp Java 端返回的 Map → JSON 反序列化结构。
//
// 业务响应都用 HTTP 200, 通过 ok 字段区分成功/失败:
//   - 成功: {ok: true, entries: [...]}
//   - 失败: {ok: false, error: "..."}
type LeaderboardResp struct {
	OK      bool    `json:"ok"`
	Error   string  `json:"error,omitempty"`
	Entries []Entry `json:"entries,omitempty"`
}
