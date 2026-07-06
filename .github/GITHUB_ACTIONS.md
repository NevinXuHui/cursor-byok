# GitHub Actions 自动编译说明

本项目已配置 GitHub Actions 自动化编译流程，支持多平台构建。

## 工作流说明

### 1. build.yml - 通用构建工作流

**触发条件：**
- 推送到 `main` 分支
- 提交 Pull Request 到 `main` 分支
- 推送以 `v` 开头的 tag（如 `v1.0.0`）
- 手动触发（支持选择是否发布 Release）

**构建平台：**
- macOS arm64 (Apple Silicon)
- macOS amd64 (Intel)
- Windows 32-bit
- Windows 64-bit
- Linux amd64

**输出：**
- 所有构建产物上传为 GitHub Artifacts
- 如果是 tag 推送或手动触发选择发布，会自动创建 GitHub Release

### 2. release.yml - 正式发布工作流

**触发条件：**
- 推送语义化版本 tag（如 `v1.0.0`, `v2.1.3`）
- 手动触发

**特点：**
- 使用项目内置的 `Taskfile.yml` release 脚本
- 在 macOS runner 上完成所有平台构建
- 自动生成 `update.json` 清单
- 自动同步 README 到发布仓库

## 使用方法

### 方式 1：推送 tag 触发自动发布

```bash
# 1. 更新 release-notes.md，填写本次发布说明
vim release-notes.md

# 2. 提交并推送代码
git add .
git commit -m "准备发布 v1.0.0"
git push

# 3. 创建并推送 tag
git tag v1.0.0
git push origin v1.0.0
```

### 方式 2：手动触发构建

1. 打开 GitHub 仓库页面
2. 进入 `Actions` 标签页
3. 选择 `Build Multi-Platform` 或 `Release` 工作流
4. 点击 `Run workflow` 按钮
5. 如果需要创建 Release，勾选 `是否创建 GitHub Release`
6. 点击运行

### 方式 3：提交代码触发构建（不发布）

```bash
# 正常提交到 main 分支即可触发构建
git add .
git commit -m "更新功能"
git push origin main
```

构建完成后，产物会保存在 GitHub Artifacts 中，可下载测试。

## 构建产物说明

| 文件名 | 平台 | 说明 |
|--------|------|------|
| `macos-arm64.dmg` | macOS Apple Silicon | M1/M2/M3 芯片 Mac |
| `macos-intel.dmg` | macOS Intel | Intel 芯片 Mac |
| `windows-64.zip` | Windows 64-bit | 64 位 Windows 系统 |
| `windows-32.zip` | Windows 32-bit | 32 位 Windows 系统 |
| `linux-amd64.tar.gz` | Linux 64-bit | 64 位 Linux 系统 |

## 配置要求

### GitHub Secrets（可选）

如果需要代码签名或特殊配置，可以在 GitHub 仓库设置中添加：

- `GITHUB_TOKEN`: 自动提供，无需手动配置
- 其他自定义 secrets（如有需要）

### 前置条件

项目已包含必要的构建配置：
- ✅ `Taskfile.yml` - 构建任务配置
- ✅ `build/` 目录 - 平台特定构建配置
- ✅ `go.mod` - Go 依赖管理
- ✅ `frontend/package.json` - 前端依赖管理

## 构建时间

参考构建时间（实际时间会因 GitHub Actions 队列而异）：

- macOS + Windows 构建：约 15-20 分钟
- Linux 构建：约 10-15 分钟
- 总计：约 20-25 分钟

## 故障排查

### 构建失败

1. 检查 Actions 日志查看详细错误
2. 确认 `release-notes.md` 不为空（release 工作流需要）
3. 确认 `go.mod` 和 `frontend/package-lock.json` 存在

### 发布失败

1. 确认 tag 格式正确（`v` + 版本号）
2. 检查 `GITHUB_TOKEN` 权限（需要 `contents: write`）
3. 确认没有重复的 tag

## 本地测试

在推送前可以本地测试构建：

```bash
# 安装 Task
# macOS: brew install go-task/tap/go-task
# Linux: sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d

# 构建当前平台
task build

# 构建所有平台（仅 macOS）
task build:all

# 准备发布（仅 macOS）
task release:prepare:darwin
```

## 版本管理

版本号在 `build/config.yml` 中定义，发布脚本会自动读取。

## 相关链接

- [GitHub Actions 文档](https://docs.github.com/actions)
- [Wails v3 文档](https://wails.io/docs/gettingstarted/installation)
- [Task 文档](https://taskfile.dev/)