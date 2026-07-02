package proxy

// redirector.go — 协议逆向轨道:环境变量重定向(对标 claude-code-router)。
//
// 背景(见 docs/ARCHITECTURE.md §四 的诚实说明):
//   MRD §5.1 点名"借鉴 claude-code-router 的 MITM 代理",但实测 claude-code-router
//   根本不是 MITM——它是本地起一个 OpenAI/Anthropic 兼容 HTTP 服务,通过环境变量
//   重定向(ANTHROPIC_BASE_URL)把闭源客户端的请求拐到本地。本文件实现同样的实用方案。
//
// 工作原理:
//   1. 用户想评测一个闭源客户端 Agent(如某个基于 Claude/GPT 的桌面工具)
//   2. 平台本地起一个 OpenAI 兼容适配服务(就是 proxy.go 的 HTTP 反向代理)
//   3. 本文件帮用户生成"环境变量注入指令",把客户端的 API 端点重定向到本地代理
//   4. 客户端的真实请求经过本地代理 → 转发到真实云端 → 捕获 → 转评测任务
//
// 与 MITM 的区别:
//   - MITM 是被动截获(需装 CA,对抗证书 pinning 难)
//   - 环境变量重定向是"客户端主动配合"(用户自己改环境变量,合规无争议)
//   - claude-code-router 用的就是后者,稳定可维护
//
// 本文件不实现(MRD 原文里激进但法律灰色的部分,降级为预留接口):
//   - TLS 指纹伪造(uTLS/JA3)
//   - 解密本地配置文件提取 Auth Token
//   - 劫持本地 IPC 通信
//   - 伪造加密签名
//   这些在 docs/ARCHITECTURE.md §四 有说明,此处仅留接口签名供未来填充。

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// RedirectConfig 是环境变量重定向的配置。
type RedirectConfig struct {
	LocalProxyURL string // 本地代理地址(如 http://127.0.0.1:9090)
	TargetCloudURL string // 真实云端地址(如 https://api.anthropic.com)
	APIKey        string // 用户提供的 API Key(透传给云端,本地不留存明文)
	Protocol      string // openai | anthropic(决定注入哪些环境变量)
}

// RedirectEnvVars 返回该协议下需要注入的环境变量映射。
//
// 不同协议的客户端读不同的环境变量:
//   - openai 协议:OPENAI_BASE_URL / OPENAI_API_KEY
//   - anthropic 协议:ANTHROPIC_BASE_URL / ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN
func (c *RedirectConfig) RedirectEnvVars() map[string]string {
	vars := map[string]string{}
	switch strings.ToLower(c.Protocol) {
	case "anthropic":
		// claude-code-router 的核心做法
		vars["ANTHROPIC_BASE_URL"] = c.LocalProxyURL
		if c.APIKey != "" {
			vars["ANTHROPIC_API_KEY"] = c.APIKey
			vars["ANTHROPIC_AUTH_TOKEN"] = c.APIKey
		}
		// 禁用一些会干扰重定向的官方行为
		vars["DISABLE_PROMPT_CACHING"] = "1"
	case "openai", "":
		fallthrough
	default:
		vars["OPENAI_BASE_URL"] = c.LocalProxyURL
		if c.APIKey != "" {
			vars["OPENAI_API_KEY"] = c.APIKey
		}
	}
	return vars
}

// ShellExportScript 返回注入环境变量的 shell 脚本(供用户复制执行)。
//
// format: sh | cmd | powershell
// 用户在自己终端跑这段脚本后,再启动被测客户端,流量就会经过本地代理。
func (c *RedirectConfig) ShellExportScript(format string) string {
	vars := c.RedirectEnvVars()
	var sb strings.Builder

	switch format {
	case "cmd":
		for k, v := range vars {
			sb.WriteString(fmt.Sprintf("set %s=%s\r\n", k, v))
		}
	case "powershell":
		for k, v := range vars {
			sb.WriteString(fmt.Sprintf("$env:%s = \"%s\"\r\n", k, v))
		}
	default: // sh / bash
		for k, v := range vars {
			sb.WriteString(fmt.Sprintf("export %s=\"%s\"\n", k, v))
		}
	}
	return sb.String()
}

// WriteEnvFile 把环境变量写入一个 .env 文件(供 Docker -e / docker-compose env_file 用)。
// 返回文件路径。
func (c *RedirectConfig) WriteEnvFile(dir string) (string, error) {
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, ".eval-redirect.env")
	var sb strings.Builder
	for k, v := range c.RedirectEnvVars() {
		sb.WriteString(fmt.Sprintf("%s=%s\n", k, v))
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0600); err != nil {
		return "", err
	}
	return path, nil
}

// InjectRedirectEnv 把重定向环境变量注入当前进程(供"平台自己拉起被测 Agent 进程"的场景)。
//
// 注意:这只影响平台后续 fork 的子进程(如 terminal 轨道 exec.Command 起的进程),
// 不会影响已经运行的进程。返回注入前的旧值(供 RestoreEnv 恢复)。
func InjectRedirectEnv(cfg RedirectConfig) map[string]string {
	old := map[string]string{}
	for k, v := range cfg.RedirectEnvVars() {
		old[k] = os.Getenv(k)
		os.Setenv(k, v)
	}
	return old
}

// RestoreEnv 恢复环境变量到注入前的状态。
func RestoreEnv(old map[string]string) {
	for k, v := range old {
		if v == "" {
			os.Unsetenv(k)
		} else {
			os.Setenv(k, v)
		}
	}
}

// ShellHint 返回当前平台推荐的 shell 格式(供前端默认选项)。
func ShellHint() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "sh"
}

// ── 预留接口(MRD §5.1 激进能力,法律灰色,暂不实现) ──
// 以下函数签名保留供未来填充,当前返回"未实现"错误,避免误用。

// ErrNotImplemented 预留能力未实现的统一错误。
var ErrNotImplemented = fmt.Errorf("该能力(TLS指纹伪造/配置解密/IPC劫持)为预留接口,见 docs/ARCHITECTURE.md §四")

// ForgeTLSFingerprint 预留:伪造 TLS ClientHello 指纹(JA3/JA4)绕过 pinning。
// 当前不实现,返回 ErrNotImplemented。
func ForgeTLSFingerprint(targetJA3 string) error {
	return ErrNotImplemented
}

// ExtractLocalCredentials 预留:从闭源客户端本地配置文件解密提取 Auth Token。
// 当前不实现,返回 ErrNotImplemented。
func ExtractLocalCredentials(clientConfigPath string) (string, error) {
	return "", ErrNotImplemented
}

// HijackIPC 预留:劫持本地 IPC 通信截获闭源客户端流量。
// 当前不实现,返回 ErrNotImplemented。
func HijackIPC(socketPath string) error {
	return ErrNotImplemented
}
