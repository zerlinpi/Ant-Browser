# Ant Enterprise Desktop UI

目标：实现类似紫鸟桌面客户端体验。

## 页面规划

```
frontend
├── login
│   登录页面
│
├── dashboard
│   店铺列表
│
├── accounts
│   账号管理
│
├── profiles
│   环境管理
│
├── sessions
│   浏览器运行状态
│
└── settings
    系统设置
```

## 首页流程

用户登录

↓

加载账号列表

↓

点击打开

↓

调用 Browser Manager

↓

启动本地 Chromium
