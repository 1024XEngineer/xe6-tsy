# 联调演示脚本

前置：xe6-tsy API 已启动；前端 `.env.local` 已配置（见 `CONFIG.md`）。

1. 打开 `http://localhost:3000`
2. （可选）设置 → 语言对，确认 `zh-CN / en-US`
3. 点击中央按钮
4. 观察状态依次变化：匿名登录 → 创建会话 → 配置语言 → 实时票据 → WebRTC → 启动传译
5. 若卡在 WebRTC/Start：阅读页面上的错误文案（常见：realtime 未监听 `:8090`，或 `LINGOW_SESSION_RUNTIME` 未启用）
6. 再次点击中央按钮结束会话
