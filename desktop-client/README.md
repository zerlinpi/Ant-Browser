# Ant Browser Enterprise Desktop Client

目标：构建类似紫鸟的桌面客户端。

## 技术架构

```
desktop-client
├── frontend        React UI
├── src-tauri       Rust native controller
├── modules
│   ├── auth        登录认证
│   ├── browser     浏览器启动管理
│   ├── profile     环境管理
│   ├── proxy       代理管理
│   └── updater     自动更新
```

## 当前阶段

Phase 1:

- 桌面客户端骨架
- 登录接口预留
- Profile管理接口预留
- 浏览器启动控制接口预留

## 后续

客户端负责：

1. 登录企业服务器
2. 获取账号列表
3. 加载浏览器环境
4. 启动隔离 Chromium
5. 上报设备状态
