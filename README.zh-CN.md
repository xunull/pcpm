# pcpm

[English](./README.md) · **简体中文**

**PC Process Manage —— 一套用来看清自己机器上进程的工具箱。**

三件工具,全部只读:

| | |
| --- | --- |
| [`pcpm forgotten`](#pcpm-forgotten) | 找出已经没人管、却还在跑的进程 —— 比如 AI 编程工具为了调试帮你起的服务,它自己退出了,那个服务却一跑就是好几天。 |
| [`pcpm ports`](#pcpm-ports) | 看你的哪些进程正占着监听中的 TCP 端口。 |
| [`pcpm watch`](#pcpm-watch) | 持续记录一个进程及其整棵树的资源占用,事后可回查 —— 包括进程已经退出之后。 |

---

## 目录

- [`pcpm forgotten`](#pcpm-forgotten) —— 以及[它凭什么准](#forgotten-凭什么准)
- [`pcpm ports`](#pcpm-ports)
- [`pcpm watch`](#pcpm-watch)
- [安装](#安装)
- [配置](#配置)
- [已知局限](#已知局限)
- [延伸阅读](#延伸阅读)

---

## `pcpm forgotten`

你让 codex 或 claude 帮忙改点东西,它起了个 dev server 让你验证效果。你关掉会话,工具没了,服务还在。几天后它依然在跑、依然占着 3000 端口,而你早已不知道它的存在。

在开发它的那台机器上,`pcpm forgotten` 从 **1113 个进程里精准挑出 4 个**。

```
$ pcpm forgotten
PID    PGID   AGE     PORTS  PROCS  DIR                        COMMAND
58714  58669  8d18h   8766   3      …/open-source/ocrserver    rtk proxy uv run uvicorn app.main:app --port 8766
60467  60465  8d18h   8767   3      …/open-source/ocrserver    rtk proxy uv run uvicorn app.main:app --port 8767
60952  60950  8d18h   8768   3      …/open-source/ocrserver    rtk proxy uv run uvicorn app.main:app --port 8768
68283  67907  21h55m  -      1      …/some-game/world-of-cc    bun ~/.bun/bin/gbrain serve
```

三个八天前在 `ocrserver` 项目里起的 `uvicorn`,至今占着 8766–8768 端口。**启动目录**那一列通常就足够让你一眼认出某个残留是哪来的。

```bash
pcpm forgotten                  # 有什么被落下了?
pcpm forgotten -o json          # 全字段,不截断
pcpm forgotten --ignore gbrain  # 压掉你故意常驻的东西
pcpm forgotten --fail-on-found  # 找到就以非 0 退出,便于脚本判断
```

### `forgotten` 凭什么准

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

### 输出字段

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

### 怎么清理

`PROCS = 3` 意味着那一行代表三个进程。**只杀根 PID 是不够的** —— 它的子孙会被过继给 init 继续跑,端口照旧占着。

要按**进程组**杀,用 `PGID`(不是 `PID`):

```bash
pgrep -ag 58669            # 先看清楚:这个组里到底有谁
kill -- -58669             # 给整组发 SIGTERM,注意前面那个减号
sleep 3 && pcpm forgotten  # 复查是否清干净
kill -9 -- -58669          # 只对赖着不走的补刀
```

那个减号不是"减" —— `kill(2)` 把负数读作「这是一个进程组」。拿 `PID` 去用会报 `no such process`,而且信号一个都没发出去。

## `pcpm ports`

列出你的**监听者(Listener)** —— 自己拥有、正在监听 TCP 的进程,一行一个。用于反过来问:「8766 端口被谁占着?」

```bash
pcpm ports
pcpm ports -o json
```

端口后带 `*` 表示绑在全部网卡上,可从外部访问。

## `pcpm watch`

`forgotten` 和 `ports` 回答的是「**有什么**」,`watch` 回答的是「**它一直在干什么**」—— 那个跑了八天的服务到底还在服务谁,还是从周二起就一直闲着?内存是不是在偷偷往上爬?

```bash
pcpm watch add 68283     # 开始监控一个进程及其整棵树
pcpm watch ls            # 在监控什么?采集器还活着吗?
pcpm watch show 68283    # 交互式界面
pcpm watch rm 68283      # 停止监控,已采集的历史保留
```

`watch add` 会在采集器没运行时把它拉起来 —— 关掉终端就停的监控算不上监控。它会**明确告诉你**它这么做了:pcpm 自己起的后台进程,绝不能变成"你不知道它在跑"的东西 —— 那恰恰是这个工具存在的理由。`pcpm watch ls` 会显示它是否存活、上次采集是什么时候;`pcpm watch daemon --stop` 停掉它。

`pcpm watch show` 打开一个会自动刷新的界面:

```
100 · bun · running · ~/proj

  70% ┤                                         ╶─────────────────────────────────────────
  60% ┤
  50% ┤
  40% ┤
  30% ┤
  20% ┤
  10% ┤
 0.0% ┤
      └┬────────────────┬───────────────┬────────────────┬───────────────┬────────────────┬
     19:00            19:12           19:24            19:36           19:48            20:00
                                  cpu — now 70%, peak 70%

 310 MB ┤                                         ╶─────────────────────────────────────────
 266 MB ┤
 221 MB ┤
   0  B ┤
        └┬────────────────┬───────────────┬────────────────┬───────────────┬────────────────┬
       19:00            19:12           19:24            19:36           19:48            20:00
                                memory — now 310 MB, peak 310 MB

PID  NAME     CPU   RSS
101  esbuild  70%   280 MB
100  bun      0.0%  30 MB

[1]5m  [2]1h* [3]24h  [4]7d    [r]refresh [q]quit
```

注意进程列表:**你指定的那个 `bun` 占用 0.0%** —— 真正在干活的是它下面的 `esbuild`。这才是常态。你认得的那条命令往往只是个包装器,所以**只盯你输入的那个 PID 会报告一个"闲置"的进程,而这棵树正在打满一个核**。pcpm 测量树里的每一个进程,并告诉你责任在谁身上。

输出被管道或重定向时会自动改印文本摘要而不是控制码;`--plain` 可强制文本,`-o json` 给出同样的数据供机器读取。

### 怎么读这些图

- **时间窗口是固定的** —— `5m`、`1h`、`24h`、`7d`,**不支持缩放和平移**。这也是为什么图表在每一行都标了数值:这个界面的用途就是让你一眼读出当前值。
- **线断开表示那段时间没采到数据** —— 机器休眠了,或者采集器没在跑。这里**故意不连线**:画一条直线过去等于宣称"那段时间数值很稳定",而事实是那段时间一无所知。
- **CPU 可以超过 100%** —— 这是一棵树,100% 指的是一个核。
- **CPU 超过 80% 会变红。**

### 存什么、存多久

pcpm 记录的是每个进程的**累计 CPU 时间**,不是百分比,在你查询时才换算成速率。这正是空洞能被诚实处理的原因:60 秒内消耗了 6 CPU 秒,报出来是 10%,而在采集时就算好百分比会记成一个 120% 的假尖峰。同时也意味着**平均窗口是在你查看时决定的,而不是写入时**。

近期历史全量保留,更早的历史被汇总成 1 分钟的桶,于是一个月的趋势依然可查,却不必保留一个月的原始数据:

| | 分辨率 | 保留 | 一棵 10 进程的树大约占 |
| --- | --- | --- | --- |
| 原始 Sample | 每 5 秒,按进程 | 48 小时 | ~13 MB |
| Rollup | 1 分钟 | 30 天 | ~19 MB |

所有数据都在一个 SQLite 文件里:`$XDG_STATE_HOME/pcpm/pcpm.db`(或 `~/.local/state/pcpm/pcpm.db`)。

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

## 配置

可选,位于 `$XDG_CONFIG_HOME/pcpm/config.yaml`(或 `~/.config/pcpm/config.yaml`),可用 `--config` 覆盖。文件不存在不算错误。

```yaml
# 按进程名做 glob 匹配。用来压掉你故意长期运行的作业,让它们不再出现在结果里。
ignore:
  - bun
  - "*.helper"

watch:
  sample_interval: 5s       # 多久采集一次
  discover_interval: 30s    # 多久重新遍历一次进程表以更新树成员
  maintenance_interval: 5m  # 多久做一次降采样与过期清理
  rollup_interval: 1m       # 降采样的桶大小
  raw_retention: 48h        # 全分辨率原始数据保留多久
  rollup_retention: 720h    # 降采样数据保留多久(30 天)
```

| 配置项 | 调大 | 调小 |
| --- | --- | --- |
| `sample_interval` | 更省空间,曲线更粗 | 曲线更细,存储按比例增加。采一棵 10 进程的树约 106 µs,CPU 不是瓶颈 |
| `discover_interval` | 更省;活不到一个周期的子进程会被完全漏掉 | 能抓到更短命的子进程。每次都要遍历整张进程表,约 27 ms |
| `raw_retention` | 能下钻到单个进程的时间跨度更长 | 数据库更小;那段时间仍由 rollup 覆盖 |

优先级为 `flag > PCPM_* 环境变量 > 配置文件 > 内置默认`。`--ignore` 是**追加**到配置列表之上,而不是替换它。

## 已知局限

- **`PORTS` 只看得到你自己的进程。** 树里若有 `sudo` 启动的成员,其端口不会被统计 —— 只会少报,不会多报。
- **降噪规则是启发式的。** 两个条件的判据是原理性的;而系统路径 / `.app` 助手 / shell 这几份排除清单可能需要随环境增补。
- **PID 复用会导致 `forgotten` 漏报。** `PGID` 本身就是一个 PID 值,如果那个已死组长的号被一个毫不相关的新进程复用,这一条就会被漏掉。方向是漏报,不会产生误报。`watch` 不受影响 —— 监控目标同时用启动时间和 PID 双重锁定。
- **`watch` 会漏掉极短命的子进程。** 在两次树发现之间生灭的进程不会被采到。在意的话把 `discover_interval` 调小。
- **`watch` 尚未采集网络流量** —— 目前只有 CPU 与内存。按进程统计网络字节没有可移植的数据源:macOS 可以通过 `nettop` 拿到,Linux 上没有免 root 的等价物。
- **仅 Linux 与 macOS。** Windows 没有进程组;容器有独立的 PID 命名空间,所以 pcpm 应在宿主机上运行。

## 延伸阅读

- [进程组与被遗忘的进程](docs/pgid-and-forgotten-processes.md) —— 为什么进程组是那个能在启动者死后依然留存的信号,以及在 macOS 和 Linux 上分别怎么查 PGID
- [架构决策记录](docs/adr/) —— 平台范围、判据为何从 `PPID == 1` 改成「进程组组长已死」、指标为何用 SQLite 而不是时序数据库、Sample 为何存累计计数器而非百分比、以及采集器为何是"单守护进程 + 以数据库为控制面"
- [`CONTEXT.md`](CONTEXT.md) —— 项目术语表

## 许可证

Apache-2.0
