# Karta 项目 Agent 规范

## 基准采样环境纪律（2026-08-15 批准，recurrence=3 污染事故提炼）

本机（i5-12400F / Windows）性能基准采样必须遵守：

1. **会话分离**：基准采样与 `-race`/全量测试分离到独立会话，间隔 ≥1 分钟；禁止在 `-race` 全量测试后立即采样（commit/调度压力残留导致系统性虚高 +15~100%）
2. **前置检查**：采样前确认主机 CPU 占用 < 10%（`Get-Counter '\Processor(_Total)\% Processor Time'`）；背景负载期间采样无效
3. **存疑裁决**：出现疑似回退时，用同会话交替 A/B（before→after→before→after）+ 反序探针裁决；以**未变更代码路径**的基准作为环境对照——对照项同步膨胀即为环境因素，非代码回归
4. **统计要求**：关键对比用 `-count≥10` + `benchstat`（p 值与方差）；单次数字或高方差（>±15%）数据不得作为回退裁决依据

## 已知平台事项

- `lifecycle_test.go` 的 POSIX 信号测试位于 `lifecycle_unix_test.go`（`//go:build !windows`），Windows 下自动排除，无需 overlay
- Windows 上 `go test -cpuprofile` 会遗留 `karta.test.exe`（已 gitignore `*.exe`），会话结束前清理
- 基线/复测产物约定目录：`B:\Temp\opencode\karta-perf\{baseline,round1,round2,round3,reports}`
