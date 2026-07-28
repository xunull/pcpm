# pcpm

[English](./README.md) · **简体中文**

**PC Process Manage —— 一套用来看清自己机器上进程的工具箱。**

三件工具,全部只读:

| | |
| --- | --- |
| [`pcpm forgotten`](#pcpm-forgotten) | 找出已经没人管、却还在跑的进程 —— 比如 AI 编程工具为了调试帮你起的服务,它自己退出了,那个服务却一跑就是好几天。 |
| [`pcpm ports`](#pcpm-ports) | 看你的哪些进程正占着监听中的 TCP 端口。 |
| [`pcpm top`](#pcpm-top) | 排出此刻最吃 CPU 的进程 —— 并标出其中哪些已经没人管了。 |
| [`pcpm watch`](#pcpm-watch) | 持续记录一个进程及其整棵树的资源占用,事后可回查 —— 包括进程已经退出之后。 |

---

## 目录

- [`pcpm forgotten`](#pcpm-forgotten) —— 以及[它凭什么准](#forgotten-凭什么准)
- [`pcpm ports`](#pcpm-ports)
- [`pcpm top`](#pcpm-top) —— 以及[它看不到什么](#top-看不到什么)
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

## `pcpm top`

此刻什么在吃 CPU,从多到少。

```console
$ pcpm top -n 6
CPU  193% of 1000% (10 cores)  ·  attributed 170%  ·  unattributed 22.8% (needs sudo)
MEM  49 GB / 64 GB

%CPU     RSS    PID  NAME                           APP     DIR
15.9  217 MB  56394  stable                         Warp    ~
13.5  615 MB  61742  claude                                 …/open-source/pcpm
11.2  217 MB  62625  WeChatAppEx Helper (Renderer)  WeChat  …/Contents/MacOS
10.0  300 MB  34442  Kimi Helper (Renderer)         Kimi    /
 8.1   23 MB  61398  pcpm-r                                 …/open-source/pcpm
 7.9   28 MB  25922  pcpm                                   …/open-source/pcpm
```

在终端里它会按间隔持续刷新,按 `q` 退出。一旦被管道或重定向接走,就只打一帧然后退出 —— 所以 `pcpm top | head`、`pcpm top -o json > f.json` 都不需要额外加开关;在终端里想只打一帧,用 `--once`。

```
q quit    [c cpu]  m memory    every 1s
```

**它要等一秒才出结果,这不是 bug。** 内核根本不存"CPU 使用率"这个数,只存"这个进程从出生到现在一共烧了多少 CPU 秒"。率只能是两次读数之差,所以 pcpm 必须读两遍再相减。任何瞬间就给出答案的工具,报的都是**终身平均** —— `ps aux` 的 `%CPU` 就是"累计 CPU ÷ 进程年龄":实测某个真实占用 26.5% 的进程,它报 14.5%。

**百分比以单核为分母。** 100% = 吃满一个核;一个跑在八个核上的进程显示 800%,表头那个 `of 1000%` 就是这台十核机器的满载值。若改用整机做分母,最常见的那种故障 —— 单线程死循环 —— 会被显示成一个看着没事的 10%。

### 它和 `top` 的区别在哪

`/usr/bin/top` 是 setuid root 的,它能看到的比 pcpm 永远能看到的多。所以 `pcpm top` 去回答一个 `top` 结构上答不了的问题:**这里面哪些,是已经没人记得为什么还在跑的?**

```console
$ pcpm top -n 400
   %CPU     RSS    PID  NAME    APP   DIR
!   0.0   21 MB  75403  bun           …/kapa-wiki/xiaochengxu-insight-wiki
!   0.0   34 MB  35722  bun           …/xunull-thinking/diandian
!   0.0   34 MB  81142  bun           …/open-source/cuwatch

! nothing is looking after this — see `pcpm forgotten`
```

判据与 [`pcpm forgotten`](#pcpm-forgotten) 完全一致,并且标记会落在这棵树的**每一个成员**上,而不只是树根 —— 真正在烧 CPU 的往往是子进程。

### 列的含义

| 列 | |
| --- | --- |
| `!` | 这个进程属于一棵没人管的进程树。没有任何一行符合时,该列不显示。 |
| `%CPU` | 以单核为分母,统计的是最近一个间隔。可以超过 100%。 |
| `RSS` | 常驻内存,用绝对值而不是"占 64 GB 的百分之几"。 |
| `NAME` | 可执行文件名。 |
| `APP` | 该进程所属的 macOS 应用 —— 取路径中**最外层**的 `.app`。一个应用包含很多进程。Linux 上不显示,不在 bundle 里的进程也没有。 |
| `DIR` | 启动目录。这是把同名进程分开的关键:这台机器上曾有两个 `claude` 的命令行**完全相同**,而它们分属四个不同的仓库。 |

`-o json` 会给出全部字段且不截断,包括表格放不下的完整命令行,以及表头那些数字(在 `cpu` 和 `memory` 下)。

### `top` 看不到什么

**这台机器上大约 70–85% 的忙碌 CPU。** 六次两秒窗口实测:84.0、86.0、82.4、69.9、82.3、84.7 个百分点。

剩下的那部分是"未归属",表头会明说,而不是悄悄摊进各行里。两个原因:

- **别人的进程返回 0,而不是报错。** macOS 上 `proc_pidinfo` 只对 root 或同 UID 的调用者给真值。这台机器上 205 个属于其他用户的进程,**全部**返回 CPU 0、RSS 0,且一个错误都没有。`ps` 和 `top` 之所以不受限,是因为 Apple 把它们做成了 setuid root(`4755` 与 `4555`);一个用 Homebrew 装的 Go 程序不是。
- **`kernel_task` 根本读不到。** 它是 PID 0,gopsutil 直接拒绝 —— 即便是 root 也一样。

与其把一堆明知为 0 的进程也排进去、从而把机器上真正最忙的那些排到榜尾(而排序恰恰是这个榜单存在的全部意义),pcpm 选择只排它能测准的,并把缺口量化出来。`sudo pcpm top` 除 `kernel_task` 外都能覆盖 —— 这正是为什么加了 `sudo` 之后未归属那个数仍然会显示,只是不再提示 `sudo`。它并不会归零,而假装它会归零,恰恰是表头最不该做的事。理由见 [ADR-0011](docs/adr/0011-unprivileged-visibility-ceiling.md)。

### `top` 和 `watch` 不是同一个工具

`pcpm top` 回答"**现在**什么忙",看完就忘。[`pcpm watch`](#pcpm-watch) 回答"这东西**一直以来**在干什么",并且记着。两者谁也不是谁的简化版。

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
68283 · bun · running · ~/proj

CPU   now 0.8%   peak 94%   ·   whole tree, 2 processes
100% │                                        ⠂⣶⣶⣶⣶⠐
     │                                        ⢸⣿⣿⣿⣿⡇
     │    ⠁                    ⠁         ⠈    ⢸⣿⣿⣿⣿⡇⠁         ⠈          ⠁
     │    ⡀        ⠁⣿⠁         ⡀         ⢠    ⣿⣿⣿⣿⣿⡇⡄         ⢠          ⡄
0.0% │⣀⣀⣀⣀⣇⣀⣀⣀⣀⣀⣀⣀⣀⣿⣿⣇⣀⣀⣀⣀⣀⣀⣀⣀⣀⣇⣀⣀⣀⣀⣀⣀⣀⣀⣀⣸⣀⣀⣀⣀⣿⣿⣿⣿⣿⣿⣇⣀⣀⣀⣀⣀⣀⣀⣀⣀⣸⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣇⣀⣀⣀⣀
     └────────────────────────────────────────────────────────────────────────
     19:00                            19:30                             20:00

MEMORY   now 1.0 GB   peak 1.0 GB
1.3 GB │
       │                                                            ⣀⣀⣠⣤⣤⣴⣶⣶⣿⣿
       │                                               ⣀⣀⣀⣤⣤⣤⣴⣶⣶⣾⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿
       │                             ⢀⣀⣀⣀⣀⣠⣤⣤⣤⣤⣶⣶⣶⣶⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿
   0 B │⣀⣀⣀⣀⣀⣀⣀⣤⣤⣤⣤⣤⣤⣤⣤⣤⣴⣶⣶⣶⣶⣶⣶⣶⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿
       └──────────────────────────────────────────────────────────────────────
       19:00                           19:30                            20:00

   PID    NAME     CPU   RSS
   68290  esbuild  9.3%  1.0 GB
   68283  bun      0.0%  30 MB

[1]5m  [2]1h* [3]24h  [4]7d    [tab]process [r]refresh [q]quit
```

这张图一眼说了两件事。**CPU 绝大部分时间闲置、只有零星几次短促的活儿** —— 说明还有人在访问这个服务,而这正是"能不能杀"的判断依据。以及**内存一路爬升、从不回落** —— 这是内存泄漏的样子。

注意进程列表:**你指定的那个 `bun` 占用 0.0%** —— 真正在干活的是它下面的 `esbuild`。这才是常态。你认得的那条命令往往只是个包装器,所以**只盯你输入的那个 PID 会报告一个"闲置"的进程,而这棵树正在打满一个核**。pcpm 测量树里的每一个进程,并告诉你责任在谁身上。

输出被管道或重定向时会自动改印文本摘要而不是控制码;`--plain` 可强制文本,`-o json` 给出同样的数据供机器读取。

### 怎么读这些图

- **实心填充是平均值,上面的点是峰值。** 7 天视图里一列覆盖好几个小时,只取平均会把"闲置一小时里的 3 秒请求"抹成 0 —— 而那 3 秒恰恰是"还有人在用"的证据。封顶点把它保住。
- **填充中断表示那段时间没采到数据** —— 机器休眠了,或者采集器没在跑。**闲置是另一回事**:它仍然有一条贴底的细线。两者不能长得一样,否则"采集器停了"会被当成"进程很安静"。
- **纵轴自适应窗口内的峰值**,所以闲置的进程也看得清。被遗忘的进程绝大多数是闲置的;如果把轴锁死在一个整核,一个占 3%、偶尔冲到 12% 的服务会变成一条直线,而那几次冲高恰恰是"还有人在用"的证据。
- **颜色跟着数值走,不跟高度走。** 半个核是渐变的中点、一个整核是顶点,所以不管轴是多少,红色永远代表同一件事。颜色因此是贴着曲线变化的,而不是横向色带。
- **颜色适配终端的能力,不适配终端的配色。** `COLORTERM` 声明真彩色就用 24 位,否则降到 256 色立方,控制台用 16 色,`NO_COLOR` 或 `TERM=dumb` 时完全不上色。坐标轴、刻度、标题**不带任何颜色**,继承终端前景色;背景永远不涂。
- **`tab` 在进程列表里移动。** 选中某个进程后,图表只画那一个进程 —— 这正是区分"忙的是包装器还是干活的"的办法。`tab` 走过最后一项回到整棵树。
- **时间窗口固定** —— `5m`、`1h`、`24h`、`7d`,不支持缩放和平移。

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

top:
  interval: 1s              # 既是刷新周期,也是每个数字的平均窗口
  number: 0                 # 0 = 按终端高度自适应;写任何其他值就是明确指定行数
  sort: cpu                 # cpu | mem
```

| 配置项 | 调大 | 调小 |
| --- | --- | --- |
| `sample_interval` | 更省空间,曲线更粗 | 曲线更细,存储按比例增加。采一棵 10 进程的树约 106 µs,CPU 不是瓶颈 |
| `discover_interval` | 更省;活不到一个周期的子进程会被完全漏掉 | 能抓到更短命的子进程。每次都要遍历整张进程表,约 27 ms |
| `raw_retention` | 能下钻到单个进程的时间跨度更长 | 数据库更小;那段时间仍由 rollup 覆盖 |
| `top.interval` | 排序更稳,但 `--once` 要等更久 | 变化反应更快,代价是数字更抖。之所以只有一个配置项,是因为刷新周期**就是**那个平均窗口 |

优先级为 `flag > PCPM_* 环境变量 > 配置文件 > 内置默认`。`--ignore` 是**追加**到配置列表之上,而不是替换它。

## 已知局限

- **`PORTS` 只看得到你自己的进程。** 树里若有 `sudo` 启动的成员,其端口不会被统计 —— 只会少报,不会多报。
- **降噪规则是启发式的。** 两个条件的判据是原理性的;而系统路径 / `.app` 助手 / shell 这几份排除清单可能需要随环境增补。
- **PID 复用会导致 `forgotten` 漏报。** `PGID` 本身就是一个 PID 值,如果那个已死组长的号被一个毫不相关的新进程复用,这一条就会被漏掉。方向是漏报,不会产生误报。`watch` 不受影响 —— 监控目标同时用启动时间和 PID 双重锁定。
- **`watch` 会漏掉极短命的子进程。** 在两次树发现之间生灭的进程不会被采到。在意的话把 `discover_interval` 调小。
- **`watch` 尚未采集网络流量** —— 目前只有 CPU 与内存。按进程统计网络字节没有可移植的数据源:macOS 可以通过 `nettop` 拿到,Linux 上没有免 root 的等价物。
- **不加 `sudo` 时,`top` 只看得到约 70–85% 的忙碌 CPU**,且永远看不到 `kernel_task`。其他用户的进程会返回 0 而不是报错,因此它们被排除在外,而不是以 0 参与排序;缺口大小由表头给出。见 [ADR-0011](docs/adr/0011-unprivileged-visibility-ceiling.md)。
- **`top` 的 `APP` 列仅限 macOS。** 它取自可执行文件路径中的 `.app` bundle,Linux 上没有对应物,该列在那里直接不显示。
- **仅 Linux 与 macOS。** Windows 没有进程组;容器有独立的 PID 命名空间,所以 pcpm 应在宿主机上运行。

## 延伸阅读

- [进程组与被遗忘的进程](docs/pgid-and-forgotten-processes.md) —— 为什么进程组是那个能在启动者死后依然留存的信号,以及在 macOS 和 Linux 上分别怎么查 PGID
- 架构决策记录([全部](docs/adr/)):
  - [ADR-0005](docs/adr/0005-detect-forgotten-by-dead-process-group-leader.md) —— 判据为何从 `PPID == 1` 改成「进程组组长已死」
  - [ADR-0007](docs/adr/0007-metrics-in-sqlite-not-a-tsdb.md) —— 指标为何用 SQLite 而不是时序数据库
  - [ADR-0008](docs/adr/0008-store-cumulative-cpu-time-not-a-percentage.md) —— Sample 为何存累计计数器而非百分比
  - [ADR-0009](docs/adr/0009-one-daemon-controlled-through-the-database.md) —— 采集器为何是「单守护进程 + 以数据库为控制面」
  - [ADR-0011](docs/adr/0011-unprivileged-visibility-ceiling.md) —— `top` 为何只排它能真正测准的进程
- [`CONTEXT.md`](CONTEXT.md) —— 项目术语表

## 许可证

Apache-2.0
