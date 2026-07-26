# 把 GitHub Action 固定到 commit SHA:pcpm 的供应链风险分析

> 本文说明 pcpm 的发布流水线为什么存在一条供应链攻击路径,"把 action 固定到 commit SHA"具体解决什么、**解决不了什么**,以及为什么当前(2026-07-25)决定暂不实施。
>
> 相关文件:[`.github/workflows/release.yml`](../.github/workflows/release.yml)、[`.github/workflows/ci.yml`](../.github/workflows/ci.yml);发布方案的取舍见 [ADR-0006](adr/0006-release-with-goreleaser.md)。
>
> **验证状态**:pcpm 自身的流水线现状为仓库内实测;GitHub Actions 的引用解析规则与官方加固建议依据 GitHub 文档;文中引用的历史事件按公开报道叙述,细节请自行复核。

---

## 1. 结论先行

一句话:

> **`uses: some/action@v6` 里的 `v6` 是别人仓库里的一个 git tag,而 tag 是可移动的 ——
> 你写的是"版本",拿到的却是"上游此刻决定给你的任意代码",
> 而这段代码运行在你的 runner 上,能读到你交给它的全部 secrets。**

pcpm 的发布任务把一个**跨仓库 PAT**(`TAP_PUSH_TOKEN`,对 `xunull/homebrew-tap` 有 `contents: write`)交给了第三方 action `goreleaser/goreleaser-action@v6`。这条链的终点不是本仓库,而是所有 `brew install pcpm` 用户的机器。

把 `@v6` 换成 `@<40 位 commit SHA>` 可以关掉"tag 被重新指向"这个入口 —— 但它**不是全套防护**,见第 6 节。

---

## 2. 现状:哪些 action 能碰到什么

| 位置 | Action | 归属 | 能拿到的凭据 | 暴露面 |
| --- | --- | --- | --- | --- |
| `release.yml:37` | `goreleaser/goreleaser-action@v6` | 第三方 | `GITHUB_TOKEN`(`contents: write`)+ **`TAP_PUSH_TOKEN`(跨仓库 PAT)** | **最高** |
| `release.yml:24,28` | `actions/checkout@v4`、`actions/setup-go@v5` | GitHub 官方 | 同一 job,同样能读到 env | 高(但归属可信) |
| `ci.yml:68,75` | `goreleaser/goreleaser-action@v6` | 第三方 | 无 secrets,`permissions: contents: read` | 低 |
| `ci.yml:24,26` | `actions/checkout@v4`、`actions/setup-go@v5` | GitHub 官方 | 无 secrets | 低 |

两点值得单独说明:

- **secrets 是 job 级可见的,不是 step 级。** `release.yml` 里 `TAP_PUSH_TOKEN` 写在 goreleaser step 的 `env:` 下,看起来只给了那一步,但同一 job 中的任何 step 都运行在同一个 runner 上,能读进程环境、能读 `$RUNNER_TEMP`、能读 checkout 出来的工作区。所以 `actions/checkout` 与 `actions/setup-go` 同样在信任边界之内 —— 只是它们归属 GitHub 官方,风险量级不同。
- **CI 的那两处几乎无所谓。** `ci.yml` 里 `permissions: contents: read` 且没有任何 secrets,即使 action 被投毒,能做的也只是污染一次干跑构建的结果。固定它是为了一致性,不是为了防护。

---

## 3. 威胁模型:可移动的引用

`uses:` 后面可以写三种东西:

| 写法 | 例子 | 可变性 |
| --- | --- | --- |
| 分支 | `@main` | 上游每次提交都变 |
| tag | `@v6`、`@v6.4.0` | **上游可随时重新指向另一个 commit** |
| commit SHA | `@a1b2c3…`(40 位) | 不可变 |

关键在于第二行。git tag 不是不可变对象,`git tag -f v6 <新commit> && git push -f --tags` 就能把它挪走 —— 事实上,**主流 action 的 `v6` 这类大版本 tag 本来就是设计成会移动的**:上游发 v6.4.1 时会把 `v6` 挪过去,好让所有写 `@v6` 的人自动拿到修复。这是特性,不是失误。

代价是:你把"这个仓库的维护者(以及任何拿到其写权限的人)在你下次发版那一刻推送的任意代码"纳入了自己的信任边界,而且没有任何 diff、没有任何评审、没有任何通知。

这不是理论上的担忧。2025 年 3 月的 `tj-actions/changed-files` 事件就是这个形状:攻击者拿到仓库写权限后,把多个版本 tag 重新指向一个恶意 commit,该 commit 会 dump runner 内存中的 secrets 到构建日志里;因为大量仓库使用 `@v<major>` 的浮动写法,受影响仓库数以万计。写死 SHA 的仓库不受影响。

---

## 4. 为什么 pcpm 尤其需要在意

一般项目被投毒,损失止于自己的仓库。pcpm 不同:

```
上游 action 被投毒
  → 读到 TAP_PUSH_TOKEN
  → 改写 xunull/homebrew-tap 的 Casks/pcpm.rb(url + sha256)
  → 所有 brew install pcpm / brew upgrade 的用户下载到攻击者的二进制
```

cask 里的 `sha256` 是**攻击者自己填的**,所以 Homebrew 的校验拦不住 —— 它校验的是"下到的东西和 cask 说的一致",不是"cask 说的东西是你发的"。这条链的终点是别人的电脑。

放大风险的两个已有特征:

- pcpm 的二进制**未做 Apple 公证**(ADR-0006),cask 还带了清除 `com.apple.quarantine` 的 `postflight` 钩子。这是为了让未签名的自建二进制能装,但同时也意味着系统层没有第二道门。
- tap 是**独立仓库**,它的提交历史不会出现在 pcpm 的 PR 流程里,被改了不容易第一时间发现。

---

## 5. SHA 固定怎么做

把可移动的 tag 换成不可变的 commit SHA,并用注释保留人类可读的版本号:

```yaml
# 之前
- uses: goreleaser/goreleaser-action@v6

# 之后
- uses: goreleaser/goreleaser-action@<40 位 commit SHA> # v6.4.0
```

要求与约定:

- **必须是完整 40 位 SHA**,缩写形式不被接受。
- 末尾的 `# v6.4.0` 注释不只是给人看的:Dependabot 与 Renovate 都认这个约定,升级时会同时替换 SHA 和注释,所以固定 SHA 并不意味着从此手工维护。
- 取 SHA 的方式(不要从网页上抄,容易抄到 tag 对象而非 commit):

  ```bash
  gh api repos/goreleaser/goreleaser-action/commits/v6.4.0 --jq .sha
  gh api repos/actions/checkout/commits/v4 --jq .sha
  ```

配套开启自动升级(否则会烂在旧版本上,反而错过上游的安全修复):

```yaml
# .github/dependabot.yml
version: 2
updates:
  - package-ecosystem: github-actions
    directory: "/"
    schedule:
      interval: weekly
```

---

## 6. 它挡不住什么(重要)

固定 SHA 只锁住了**action 的仓库内容**。pcpm 的发布链上还有两处不受它约束:

1. **运行时下载的 goreleaser 二进制。** `release.yml` 里写的是:

   ```yaml
   with:
     distribution: goreleaser
     version: "~> v2"
   ```

   goreleaser-action 的职责就是在 runner 上**去下载**一个 goreleaser 可执行文件再运行它。`~> v2` 是个浮动约束,即使 action 本身钉死在某个 SHA,它每次仍会取回当时最新的 v2.x 二进制。要一并收紧,得把 `version` 写成精确版本(如 `"2.17.2"`),再逐次手动升级。

2. **action 自身的传递依赖。** 一个 JavaScript action 的 `node_modules` 通常已随仓库提交(所以被 SHA 覆盖),但 composite action 里的 `run:` 步骤可能 `curl | sh` 别的东西 —— 那部分依然在信任边界之外。

所以准确的表述是:**SHA 固定把"可被无声篡改的入口"从三个减到一个,不是把风险降到零。** 把它当成"做了就安全了"是错的。

---

## 7. 可与之组合的其他手段

按性价比排序,前两项 pcpm **已经做了**:

| 手段 | 作用 | pcpm 现状 |
| --- | --- | --- |
| 最小权限 token | 不把 PAT 当 `GITHUB_TOKEN` 用,PAT 仅对 tap 有 `contents: write` | ✅ 已做(`release.yml:43-47`) |
| workflow 级 `permissions` 收紧 | CI 只读,写权限只出现在发布流程 | ✅ 已做(`ci.yml:12`) |
| PAT 设置过期时间并轮换 | 缩短凭据被窃后的可用窗口 | 待确认(fine-grained PAT 默认有过期时间) |
| SHA 固定 + Dependabot | 关掉"tag 被重新指向" | ❌ 本文讨论的对象 |
| GitHub Environment + required reviewer | 发布 job 取用 secrets 前需人工批准,异常运行会卡住 | ❌ 未做 |
| 精确固定 goreleaser 版本 | 关掉第 6 节的第 1 项 | ❌ 未做 |
| 构建产物 attestation(`actions/attest-build-provenance`) | 让用户能验证二进制确实由本仓库的这次 workflow 产出 | ❌ 未做,可独立开票 |

最后一项方向不同但值得一提:前面几项都是**防止 tap 被改**,attestation 是**即使被改也能被发现**。

---

## 8. 当前决定

**2026-07-25:暂不实施。** 记录理由,以免日后重新讨论:

- 目前只有作者一人有仓库写权限,发布靠手工打 tag 触发,没有自动发布路径。
- 用第三方 action 的地方只有一个(goreleaser-action),归属相对可信、活跃维护。
- 只做一半(固定 SHA 但不固定 goreleaser 版本)带来的是**安全感而非安全**,不如先记录清楚。

**满足以下任一条件时应重新评估:**

- 仓库新增协作者,或开启了任何形式的自动发布 / 自动合并;
- 发布流水线引入第二个第三方 action;
- pcpm 的 Homebrew 安装量明显增长(下游影响面变大);
- 上游 goreleaser-action 或其所属组织出现安全事件。

真要做的时候,建议一次性做完这三件事,而不是只钉 SHA:

1. 四处 `uses:` 全部固定到 40 位 SHA,带 `# vX.Y.Z` 注释;
2. `version: "~> v2"` 改为精确版本;
3. 加 `.github/dependabot.yml`,让升级仍然自动开 PR。
