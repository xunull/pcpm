# 进程组(PGID):识别被遗忘进程的关键信号

> 本文说明为什么**进程组 ID(PGID)**是识别"被遗忘进程"的关键,以及在 **macOS** 和 **Linux** 上分别如何获取它。
>
> 判据本身的推导见 [ADR-0005](adr/0005-detect-forgotten-by-dead-process-group-leader.md)。
>
> **验证状态**:macOS 部分在 Darwin 25.5.0 上实测;Linux 部分依据 `proc(5)`、`getpgid(2)` 规范,文末给出可自行复核的命令。

---

## 1. 结论先行

**是的,PGID 是关键。** 一句话概括:

> **PPID 记录"谁生了我",这条线索在父进程死后就断了;
> PGID 记录"我属于哪个作业",这个痕迹在启动者死后依然留在进程身上。**

被遗忘的进程,恰恰就是那些**启动者已经消失**的进程。所以任何依赖 PPID 的判据都注定失效 —— 父进程一死,PPID 就被重置成 1(或某个 subreaper),信息被抹掉了。而 PGID 不会被重置:它仍然指向那个**已经不存在的组长 PID**,这个"悬空引用"本身就是证据。

---

## 2. 为什么不是 PPID

最直觉的想法是 `PPID == 1`(父进程死了,被过继给 init)。实测数据否定了它:

| 指标 | 数量 | 占比 |
|---|---:|---:|
| 进程总数(macOS 快照) | 1113 | — |
| `PPID == 1` | **686** | **62%** |
| 最终真正被遗忘的 | 4 | 0.36% |

原因:macOS 上 launchd(PID 1)本来就是**几乎所有东西**的直接父亲 —— 守护进程、GUI 应用、XPC 服务、应用助手、Widget 扩展、被收养的 shell。Linux 上 systemd 同理。

**`PPID == 1` 说明的是"我的父进程没了",但完全没说明"是否还有人在管我"。** 这两件事不是一回事:一个规规矩矩的守护进程,父进程就是 init,但它被 init 好好托管着,并没有被遗忘。

---

## 3. 概念澄清:PID / PPID / PGID / SID

Unix 的进程有四个相关标识,理解它们的区别是理解判据的前提:

| 标识 | 含义 | 父进程死后会变吗 |
|---|---|---|
| **PID** | 进程自己的编号 | 不变 |
| **PPID** | 父进程编号 | **会被重置为 1**(或 subreaper) |
| **PGID** | 所属**进程组**的编号 = 该组**组长的 PID** | **不变** |
| **SID** | 所属**会话**的编号 = 会话首进程的 PID | 不变 |

### 四者共用同一个编号空间

注意上表的一个关键事实:**PGID 和 SID 本身就是 PID 值** —— 分别是组长、会话首进程的 PID。也就是说这四个字段全都是 PID,共用一个编号空间。

由此可以**把 PGID 当成 PID 去查**,结果有三种:

| 情形 | 现象 | 含义 |
|---|---|---|
| 组长活着 | 查到的就是组长本身 | 有效引用 |
| **组长已死** | **查不到任何进程** | **悬空引用 ← 判据 1 就是在检测它** |
| 自任组长 | `PGID == PID`,查到的是自己 | 引用永远有效 |

```bash
ps -p "$(ps -o pgid= -p 58714 | tr -d ' ')"    # 无输出 = 组长已死
```

这个"编号空间共用"的事实还解释了另外两件事:`kill` 为什么要用负号(见 §9),以及 PID 复用为什么会造成漏报(见 §8.2)。

### 进程组是什么

进程组是**作业控制(job control)**的单位。你在 shell 里敲一条命令(哪怕是 `a | b | c` 这样的管道),shell 会把这一整条命令的所有进程放进**同一个进程组**,这样按下 `Ctrl-C` 时信号可以一次性发给整组。

- 组长(process group leader)= PID 等于 PGID 的那个进程;
- **组长退出后,进程组不会解散**,其余成员的 PGID 仍然是那个已消失的 PID 值;
- 这就形成了一个**悬空引用** —— 而它正是我们要的信号。

### 关键:`setsid()` 造成的结构性差异

这是整个判据成立的根基:

**规矩的守护进程**在启动时会调用 `setsid()`(或 `daemon()`)把自己从原来的会话与进程组中摘出来,**自立为新组的组长**。于是:

```
PGID == PID   →  组长就是它自己  →  只要它活着,组长就活着
```

**它在结构上永远不可能命中"组长已死"这个判据。** 这不是靠白名单排除的,是原理上的必然。

实测验证(全部 `pgid == pid`):

| 进程 | PID | PGID |
|---|---:|---:|
| `redis-server` | 3698 | 3698 |
| `postgres` | 3721 | 3721 |
| `mysqld` | 3703 | 3703 |
| `com.docker.vmnetd` | 840 | 840 |
| `autofsd` | 613 | 613 |

**而被工具随手 spawn 的子进程不会做这件事** —— 它直接继承启动者(codex / IDE / shell 作业)的进程组。启动者一死,组长就成了一个不存在的 PID:

| 进程 | PID | PGID | 组长状态 |
|---|---:|---:|---|
| `rtk proxy uv run uvicorn …8766` | 58714 | 58669 | **已死** |
| `bun … gbrain serve` | 68283 | 67907 | **已死** |

---

## 4. 完整判据

光有"组长已死"还不够,会捞进树内部的成员(它们的父进程还活着,有人管)。完整判据是两条:

1. **进程组的组长已死** —— 启动我的那个作业消失了;
2. **父进程不在这个进程组里** —— 我是这个作业被遗弃后残留的**树根**,不是树内部的后代。

实测第 2 条的必要性:只用第 1 条(且去噪后)命中 55 个,其中约 50 个是 `gitstatusd` —— 它们的父 shell 还活着,只是和它们同在一个组长已死的组里。加上第 2 条后被正确排除。

> **为什么第 2 条不写成 `PPID == 1`**:两种写法在实测中结果一致,但 Linux 有 **subreaper** 机制(`prctl(PR_SET_CHILD_SUBREAPER)`,典型是 `systemd --user`),孤儿会被过继给它而非 PID 1。写成"父不在同组"在这种情况下依然成立。

---

## 5. macOS 上获取 PGID

> ⚠️ **macOS 没有 `/proc` 文件系统**(已实测:`ls /proc` → No such file or directory)。所有基于读文件的做法在 macOS 上都不可用。

### 5.1 命令行:`ps -o pgid`(最简单)

```bash
# 查单个进程
ps -o pid,ppid,pgid,command -p 58714

#   PID  PPID  PGID COMMAND
# 58714     1 58669 rtk proxy uv run uvicorn …

# 只取 PGID 的值(= 后面不留表头)
ps -o pgid= -p 58714        # → 58669

# 全部进程一次拿到
ps -axo pid,ppid,pgid,stat,etime,command
```

### 5.2 系统调用:`getpgid(2)`(推荐)

POSIX 标准调用,macOS 原生支持:

```c
#include <unistd.h>

pid_t pgid = getpgid(pid);   // 失败返回 -1,并设置 errno
```

Go 语言里标准库已经封装好:

```go
import "syscall"

pgid, err := syscall.Getpgid(pid)   // pid 为 int
```

**这是 pcpm 采用的方式**,原因见第 7 节。

### 5.3 `libproc`(需要 cgo)

如果本来就要通过 `libproc` 批量拿进程信息,PGID 就在 `proc_bsdinfo` 结构体里:

```c
#include <libproc.h>

struct proc_bsdinfo bsd;
proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &bsd, sizeof(bsd));
pid_t pgid = bsd.pbi_pgid;    // 同时还有 pbi_ppid、pbi_uid 等
```

### 5.4 `sysctl`(一次性枚举全部进程)

`KERN_PROC_ALL` 返回一组 `struct kinfo_proc`,PGID 在 `kp_eproc.e_pgid`。适合"一次系统调用拿到全机进程表"的场景,但结构体繁琐、需要 cgo 或手工定义。

---

## 6. Linux 上获取 PGID

Linux 的选择更多,因为有 `/proc`。

### 6.1 `/proc/<pid>/stat` 的第 5 个字段(最常用)

依据 `proc(5)`,`/proc/<pid>/stat` 的字段顺序是:

```
(1) pid   (2) comm   (3) state   (4) ppid   (5) pgrp   (6) session   (7) tty_nr   (8) tpgid  ...
```

**第 5 个字段 `pgrp` 就是 PGID。**

> ⚠️ **解析陷阱**:第 2 个字段 `comm` 用括号包着,而**进程名里可以包含空格和右括号**(例如 `(my prog) x)`)。直接按空格切会错位。正确做法是**从最后一个 `)` 之后开始切分**。

正确的 shell 写法:

```bash
# 去掉直到最后一个 ") " 的部分,剩下的第 3 个字段就是 pgrp
sed 's/.*) //' /proc/58714/stat | awk '{print $3}'
```

切完之后字段变成:`$1=state`、`$2=ppid`、`$3=pgrp`。

Go 里的正确解析:

```go
data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
if err != nil {
    return 0, err
}
s := string(data)
i := strings.LastIndex(s, ")")          // 关键:最后一个右括号
fields := strings.Fields(s[i+2:])       // 跳过 ") "
pgid, err := strconv.Atoi(fields[2])    // [0]=state [1]=ppid [2]=pgrp
```

**优点**:一次读文件同时拿到 ppid、pgid、session、state、启动时间等,批量扫描时比逐个系统调用更省。

### 6.2 `/proc/<pid>/status` 的 `NSpgid`

```bash
grep NSpgid /proc/58714/status      # → NSpgid:  58669
```

字段名易读,不用数位置。但要注意:

- `NSpgid` 是 Linux **4.1 及以后**才有的;
- 它是**命名空间相关**的值(容器内看到的是容器内的编号)。在初始命名空间里与 `pgrp` 一致。

### 6.3 命令行:`ps -o pgid`

与 macOS 完全一致(POSIX 定义的输出格式):

```bash
ps -o pid,ppid,pgid,command -p 58714
ps -o pgid= -p 58714
ps -eo pid,ppid,pgid,stat,etime,cmd      # Linux 用 -e,macOS 用 -ax
```

### 6.4 系统调用:`getpgid(2)`

与 macOS 相同,C 和 Go 的写法完全一样:

```go
pgid, err := syscall.Getpgid(pid)
```

---

## 7. 跨平台选型:pcpm 的做法

| 方案 | macOS | Linux | 依赖 | 评价 |
|---|:---:|:---:|---|---|
| `syscall.Getpgid` | ✅ | ✅ | 标准库 | **采用** |
| `/proc/<pid>/stat` | ❌ | ✅ | 无 | 只覆盖 Linux,需另写 macOS 分支 |
| `ps -o pgid` | ✅ | ✅ | fork 外部进程 | 要解析文本,脆弱 |
| `libproc` / `sysctl` | ✅ | ❌ | cgo | 只覆盖 macOS |
| **gopsutil** | — | — | — | **不提供 PGID** |

**注意最后一行**:pcpm 其余进程信息全部走 `gopsutil`(`Ppid()`、`Uids()`、`Name()`、`Cmdline()`、`Cwd()`、`CreateTime()`),但**它没有 `Pgid()` 方法**。而 PGID 恰恰是整个判据的命脉,所以这一项必须单独取。

最终实现(`internal/proc/collect.go`):

```go
import (
    "syscall"
    "github.com/shirou/gopsutil/v4/process"
)

// 进程组读不到时,当作它自任组长
pgid, err := syscall.Getpgid(int(p.Pid))
if err != nil {
    pgid = int(p.Pid)
}
```

选它的理由:**唯一一个跨两个目标平台、标准库自带、零外部依赖**的方案。`getpgid` 只是读进程组归属,**不需要特权**,所以扫描全机进程不用 sudo。

---

## 8. 陷阱与防御

### 8.1 扫描期间进程退出(竞态)

`getpgid` 会失败(`ESRCH`)。**不要直接跳过这个进程** —— 跳过意味着它从"存活集合"里消失,于是**同组的其他进程会误以为组长已死**,产生误报。

pcpm 的处理是**保守降级**:把它的 PGID 当作等于自己的 PID。这样它自己不会被误报(自任组长永远不命中判据),同时它仍留在存活集合里充当活着的组长。

> 这是 code review 时发现并修掉的一条真实误报路径。

### 8.2 PID 复用

这条局限直接源于"PGID 就是一个 PID 值"(见 §3):判据 1 的实现方式,就是**拿 PGID 当 PID 去查,查不到就认为组长死了**。

问题在于:组长退出后,内核**可能把它那个 PID 重新分配给一个全新的、毫不相关的进程**。

```
原本:   PGID 58669 → 查不到 → 判定「组长已死」→ 正确报出
复用后: PGID 58669 → 查得到(新进程恰好拿到这个号)→ 误判「组长在世」→ 漏掉
```

- 影响方向是**漏报(false negative)而非误报** —— 只会少报,不会冤枉好人;
- 概率低:Linux 默认 `pid_max` 通常是 32768 起(可调至 4194304),要撞上必须让 PID 号绕完一整圈又恰好落回那个值;
- **要彻底消除**,需要在"查得到组长"时再比对**启动时间**:真正的组长其创建时间必然早于组内成员;若那个 PID 对应的进程比成员还年轻,说明它是复用后的新进程,组长其实已死。

pcpm 目前**未做**这一步 —— 需要为每个候选额外读一次创建时间,收益(消除一个低概率漏报)与复杂度不成比例。若将来发现实际漏报,这是第一个该补的地方。

### 8.3 性能

`getpgid` 是每进程一次系统调用。1113 个进程 = 1113 次调用,实测无感知延迟。

如果将来要优化 Linux 路径,可改为读 `/proc/<pid>/stat`,**一次读取同时拿到 ppid + pgid + 启动时间**,把三次数据获取合成一次 I/O。

### 8.4 容器内的语义

容器里 PID 命名空间是独立的,PID 1 是容器的入口进程而非宿主 init。`NSpgid` 反映的也是命名空间内的编号。pcpm 设计为在**宿主机**上运行。

---

## 9. 自己动手复现验证

想亲眼看到这个信号,可以人为造一个"被遗忘的进程"。

> ⚠️ **常见的错误做法**:直接写 `bash -c 'sleep 300 &'` 是**造不出来**的。非交互场景下 `bash` 不会自立进程组,而是**继承调用者的进程组** —— 那个组长(你的终端 shell)还活着,所以 `sleep` 并没有被遗忘。实测确认过这一点。
>
> 要造出来,中间那个进程必须**先成为进程组组长**(调用 `setsid()`),再启动子进程,然后自己退出。

**Linux**(自带 `setsid` 命令):

```bash
setsid bash -c 'sleep 300 &'
```

**macOS**(⚠️ 默认**不带** `setsid` 命令,已实测;用 Python 调用 `os.setsid()`,该写法两个平台通用):

```bash
python3 -c "import os,subprocess; os.setsid(); subprocess.Popen(['sleep','300'])"
```

原理:`os.setsid()` 让 python 自立为新会话与新进程组的**组长**(PGID = python 的 PID),`sleep` 继承这个进程组,随后 python 立刻退出 —— 于是 `sleep` 的组长变成一个已消失的 PID。

查看它(两个平台通用):

```bash
victim=$(pgrep -f '^sleep 300$' | tail -1)
ps -o pid,ppid,pgid,command -p "$victim"
#   PID  PPID  PGID COMMAND
# 80354     1 80343 sleep 300        ← PGID 80343 指向已退出的 python
```

验证组长确实不在:

```bash
ps -p 80343       # 无输出 = 组长已死 ✓
```

实测 pcpm 能立刻抓到它:

```
PID    PGID   AGE  PORTS  PROCS  DIR                  COMMAND
80354  80343  1s   -      1      …/open-source/pcpm   sleep 300
```

用 pcpm 直接确认:

```bash
pcpm forgotten
pcpm forgotten -o json | jq '.[] | {pid, pgid, cwd, procs}'
```

### 清理

```bash
kill <PID>              # 只杀根;树里的子孙会被过继给 init 继续跑
kill -- -<PGID>         # 连根拔起:杀整个进程组(注意 PGID 前的负号)
pkill -P <PID>          # 逐层清理某个进程的直接子进程
```

`PGID` 可从 `pcpm forgotten -o json` 的 `pgid` 字段取得。

> ⚠️ **别把 PID 当 PGID 用**。`pcpm forgotten` 的表格里 `PID` 与 `PGID` 是**相邻的两列,值不一样** —— 清理整棵树要用 `PGID`。拿 `PID` 会报 `no such process`(信号一个都没发出去)。

### 那个负号是什么意思

既然 PID 与 PGID **共用同一个编号空间**(见 §3),单看数字 `58669` 就无法判断你指的是"进程 58669"还是"进程组 58669"。POSIX 用**符号**来消歧:

```c
kill(58669, sig)     // 正数 → 只发给 PID 为 58669 的那一个进程
kill(-58669, sig)    // 负数 → 发给 PGID 为 58669 的整个进程组
```

**所以那个负号不是"减号",而是一个类型标记**,意思是"这个数字请按进程组解释"。命令行的 `kill -- -58669` 只是该内核约定的直接映射(`--` 用来结束选项解析,否则 `-58669` 会被当成信号编号)。

两个保留语义要当心:

```bash
kill -- -0     # kill(0, …)  → 发给「调用者自己的进程组」→ 干掉自己的终端会话
kill -- -1     # kill(-1, …) → 发给「有权限发送的所有进程」→ 灾难
```

**组长已死不影响按组发信号**:进程组只要还有活着的成员就存在,PGID 此时只是个编号。而我们要清理的进程组,组长本来就全是死的 —— 那正是它们被判定为"被遗忘"的原因。

---

## 参考

- `getpgid(2)` — POSIX,macOS 与 Linux 均实现
- `proc(5)` — Linux `/proc/<pid>/stat` 字段定义
- `setsid(2)` / `daemon(3)` — 守护进程自立进程组的机制
- `ps(1)` — `-o pgid` 输出格式(POSIX 定义)
- [ADR-0005](adr/0005-detect-forgotten-by-dead-process-group-leader.md) — pcpm 采用该判据的决策记录
