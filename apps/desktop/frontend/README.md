# Frontend

Vue 3 + TypeScript + Vite 前端固定放在此目录。本次只冻结目录位置，不安装依赖、
不生成页面，也不实现业务接口。

计划结构：

```text
frontend/
├── public/          # 不经构建处理的静态文件
├── src/
│   ├── app/         # 应用启动、路由和全局 provider
│   ├── assets/      # 由构建工具处理的图片、字体等资源
│   ├── components/  # 跨功能复用的无业务状态组件
│   ├── features/    # 按业务能力组织的页面逻辑和组件
│   ├── pages/       # 路由页面及页面级组合
│   ├── services/    # 后端 API、Wails bridge 和事件客户端
│   ├── stores/      # 跨页面客户端状态
│   ├── styles/      # 全局样式、tokens 和主题入口
│   └── types/       # 仅前端内部使用的类型
└── tests/
    └── e2e/         # 桌面关键流程端到端测试
```

约束：

- 后端 contract 生成的 TypeScript 类型从 `packages/contracts` 引入，不在前端复制。
- 七模块权威状态属于 `apps/api`，前端 store 只保存展示和交互状态。
- `features` 可以依赖 `components`、`services` 和 `stores`；共享层不得反向依赖具体 feature。
