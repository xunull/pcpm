# pcpm

[English](./README.md) · **简体中文**

**找出那些已经没人管、却还在跑的进程 —— 比如 AI 编程工具为了调试帮你起的服务,它自己退出了,那个服务却一跑就是好几天。**

你让 codex 或 claude 帮忙改点东西,它起了个 dev server 让你验证效果。你关掉会话,工具没了,服务还在。几天后它依然在跑、依然占着 3000 端口,而你早已不知道它的存在。

`pcpm forgotten` 找的就是这类进程。在开发它的那台机器上,**1113 个进程里精准挑出 4 个**。

```
$ pcpm forgotten
PID    PGID   AGE     PORTS  PROCS  DIR                        COMMAND
58714  58669  8d18h   8766   3      …/open-source/ocrserver    rtk proxy uv run uvicorn app.main:app --port 8766
60467  60465  8d18h   8767   3      …/open-source/ocrserver    rtk proxy uv run uvicorn app.main:app --port 8767
60952  60950  8d18h   8768   3      …/open-source/ocrserver    rtk proxy uv run uvicorn app.main:app --port 8768
68283  67907  21h55m  -      1      …/some-game/world-of-cc    bun ~/.bun/bin/gbrain serve
```

三个八天前在 `ocrserver` 项目里起的 `uvicorn`,至今占着 8766–8768 端口。**启动目录**那一列通常就足够让你一眼认出某个残留是哪来的。

---

## 目录

- [它凭什么准](#它凭什么准)
- [安装](#安装)
- [快速开始](#快速开始)
- [命令](#命令)
- [输出字段](#输出字段)
- [怎么清理](#怎么清理)
- [配置](#配置)
- [已知局限](#已知局限)
- [延伸阅读](#延伸阅读)

---

## 它凭什么准

最直觉的判据 ——「父进程死了,所以 `PPID == 1`」—— **不成立**。macOS 上 launchd 本来就是几乎所有东西的父亲:开发机上 **1113 个进程里有 686 个**满足这条规则。它只说明"我的父进程没了",完全没说明"是否还有人在管我"。

真正能说明问题的是**进程组**。

规矩的守护进程在启动时会调用 `setsid()`,把自己变成**自己那个进程组的组长**,于是 `PGID == PID` —— 只要它活着,组长就活着:

| 进程 | PID | PGID |
| --- | ---: | ---: |
| `redis-server` | 3698 | 3698 |
| `postgres` | 3721 | 3721 |
| `mysqld` | 3703 | 3703 |

而被终端或 AI 工具随手 spawn 出来的进程不会做这件事 —— 它继承的是启动者那个作业的进程组。启动者一退出,这个进程组的组长就变成了一个**已经不存在的 PID**。所以 pcpm 在两个条件同时成立时才报告:

1. **进程组的组长已死** —— 启动它的那个作业消失了;
2. **父进程不在这个进程组里** —— 它是被遗弃后残留下来的**树根**,而不是树内部的某个后代。

条件 1 让正规守护进程在**结构上不可能**被误报 —— 这不是一份需要长期维护的黑名单。条件 2 则排除了约 50 个 `gitstatusd`(它们的父 shell 还活着,只是同处在一个组长已死的组里)。在此之上,系统路径守护进程、`.app` 包内的 GUI 助手、以及 shell 本身会被当作噪音过滤掉。

pcpm 是**只读**的:它只负责报出来,杀不杀由你决定。

## 安装

### Homebrew

```bash
brew tap xunull/tap
brew trust xunull/tap   # Homebrew 6.x 起:不信任第三方 tap 会拒绝安装
brew install pcpm
```

macOS 与 Linux 都可用 —— pcpm 以 cask 形式分发,用的是可移植的 `binary` stanza。

### go install

```bash
go install github.com/xunull/pcpm@latest
```

### 从源码构建

```bash
git clone https://github.com/xunull/pcpm.git
cd pcpm
go build -o pcpm .
```

仅支持 Linux 与 macOS。Windows 没有进程组这套模型,核心概念不成立(见 [ADR-0001](docs/adr/0001-platform-scope-and-gopsutil.md))。

## 快速开始

```bash
pcpm forgotten          # 有什么被落下了?
pcpm ports              # 我的哪些进程在监听 TCP 端口?
pcpm version            # 当前是哪个构建?
```

## 命令

### `pcpm forgotten`(别名 `forgot`)

列出被遗忘的进程,**一行一棵树**,按存活时长从老到新排 —— 跑得越久越可疑。

```bash
pcpm forgotten
pcpm forgotten -o json                    # 全字段,不截断
pcpm forgotten --ignore gbrain            # 压掉你故意常驻的东西
pcpm forgotten --fail-on-found            # 找到就以非 0 退出,便于脚本判断
```

### `pcpm ports`(别名 `listen`)

列出你的**监听者(Listener)** —— 自己拥有、正在监听 TCP 的进程,一行一个。用于反过来问:「8766 端口被谁占着?」

```bash
pcpm ports
pcpm ports -o json
```

端口后带 `*` 表示绑在全部网卡上,可从外部访问。

## 输出字段

`pcpm forgotten` 一行代表**一棵树**,不是一个进程:

| 字段 | 含义 |
| --- | --- |
| `PID` | 这棵树的根进程 |
| `PGID` | 它所属的进程组 —— **清理时要用的是这个**,它和 `PID` 不是同一个值 |
| `AGE` | 已运行时长 |
| `PORTS` | 树中**任意进程**持有的监听端口;`*` 表示对外暴露,`-` 表示没有 |
| `PROCS` | 这棵树里的进程总数(含根自己) |
| `DIR` | 启动目录 —— 通常是最快认出它是什么的线索 |
| `COMMAND` | 完整命令行,按终端宽度截断(`-o json` 里不截断) |

## 怎么清理

`PROCS = 3` 意味着那一行代表三个进程。**只杀根 PID 是不够的** —— 它的子孙会被过继给 init 继续跑,端口照旧占着。

要按**进程组**杀,用 `PGID`(不是 `PID`):

```bash
pgrep -ag 58669            # 先看清楚:这个组里到底有谁
kill -- -58669             # 给整组发 SIGTERM,注意前面那个减号
sleep 3 && pcpm forgotten  # 复查是否清干净
kill -9 -- -58669          # 只对赖着不走的补刀
```

那个减号不是"减" —— `kill(2)` 把负数读作「这是一个进程组」。拿 `PID` 去用会报 `no such process`,而且信号一个都没发出去。

## 配置

可选,位于 `$XDG_CONFIG_HOME/pcpm/config.yaml`(或 `~/.config/pcpm/config.yaml`),可用 `--config` 覆盖。文件不存在不算错误。

```yaml
# 按进程名做 glob 匹配。用来压掉你故意长期运行的作业,让它们不再出现在结果里。
ignore:
  - bun
  - "*.helper"
```

优先级为 `flag > PCPM_* 环境变量 > 配置文件 > 内置默认`。`--ignore` 是**追加**到配置列表之上,而不是替换它。

## 已知局限

- **`PORTS` 只看得到你自己的进程。** 树里若有 `sudo` 启动的成员,其端口不会被统计 —— 只会少报,不会多报。
- **降噪规则是启发式的。** 两个条件的判据是原理性的;而系统路径 / `.app` 助手 / shell 这几份排除清单可能需要随环境增补。
- **PID 复用会导致漏报。** `PGID` 本身就是一个 PID 值,如果那个已死组长的号被一个毫不相关的新进程复用,这一条就会被漏掉。方向是漏报,不会产生误报。
- **仅 Linux 与 macOS。** Windows 没有进程组;容器有独立的 PID 命名空间,所以 pcpm 应在宿主机上运行。

## 延伸阅读

- [进程组与被遗忘的进程](docs/pgid-and-forgotten-processes.md) —— 为什么进程组是那个能在启动者死后依然留存的信号,以及在 macOS 和 Linux 上分别怎么查 PGID
- [架构决策记录](docs/adr/) —— 平台范围、为什么只读、以及判据为何从 `PPID == 1` 改成「进程组组长已死」
- [`CONTEXT.md`](CONTEXT.md) —— 项目术语表

## 许可证

Apache-2.0
