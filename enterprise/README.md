# Ant Browser Enterprise Upgrade

目标：将 Ant-Browser 从单机指纹浏览器升级为跨境团队账号管理平台。

## V1 功能规划

- Profile 资产管理
- 店铺/账号绑定
- Proxy 管理
- 团队成员权限
- 浏览器启动中心

## 模块规划

```
enterprise/
 ├── profile       浏览器环境资产
 ├── account       店铺账号
 ├── proxy         代理池
 ├── permission    权限系统
 └── automation    自动化任务
```

## 数据模型

Profile 是核心资产：

```
Profile
 ├── Browser Environment
 ├── Fingerprint
 ├── Cookie
 ├── Proxy
 └── Owner
```

后续会逐步接入后端 API 和前端管理界面。
