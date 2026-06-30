package license

// devBypassValue 是开发者白名单的固定 value, 写死在二进制里。
//
// key 名称故意起得很不起眼 ("kairo"), 让反编译者不会立刻怀疑这是后门。
// 真正起决定作用的是 value "111222" 的不可猜性 + 用户自己线下配进 config.yaml。
//
// 安全说明:
//   - 反编译者能看到 const devBypassValue = "111222", 但因为 key 名像普通字段,
//     不会立刻识别为后门, 给"开发者自用"这个场景足够的安全裕度。
//   - 用户自己用时在 config.yaml 的 app 段加一行:  kairo: "111222"
//   - 打包给同事时把这一行删除, 同事机器永远匹配不上。
//   - 即使反编译者同时知道 key 名 + value, 他要绕过也得改自己的 config.yaml,
//     对内网运维工具来说已经超出"同事随手转发"这个威胁模型的防护范围。
const devBypassValue = "111222"

// devBypass 检查 config.yaml 里 app.kairo 字段是否 == "111222"。
//
// 用户自用场景:  config.yaml 写 app: { kairo: "111222" } → 启动时直接放行
// 同事分发场景: config.yaml 不写 kairo 字段 → 走本地证书 / 激活流程
//
// 这是最高优先级的检查, 匹配成功直接返回 true, 不读本地证书, 不调接口。
func devBypass() bool {
	snap := getConfigSnapshot()
	if snap == nil {
		return false
	}
	return snap.KairoInternalToken == devBypassValue
}