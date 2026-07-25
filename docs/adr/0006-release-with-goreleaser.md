# Release with GoReleaser, not a hand-rolled formula generator

The sibling project `xunull/inhomo` publishes to the same Homebrew tap (`xunull/homebrew-tap`) with a hand-written `tools/brewgen` plus a **four-runner native build matrix**. That shape is forced by its DuckDB driver: the driver ships per-platform prebuilt static libraries (GNU libstdc++ / Apple libc++ ABI), so CGO is mandatory, cross-compilation fails to link, and GoReleaser has no prebuilt builder for it.

**pcpm has none of those constraints.** It is pure Go — verified: it builds with `CGO_ENABLED=0`, and all four targets (`darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`) cross-compile from a single machine. So pcpm releases with **GoReleaser v2**: one runner cross-compiling every target, and GoReleaser publishing the Homebrew artifact to the tap — rather than porting inhomo's `brewgen`.

## Published as a cask, not a formula

GoReleaser 2.17 **deprecates `brews:`** — `goreleaser check` fails outright on it — and points at `homebrew_casks:` instead. pcpm therefore lands in the tap as `Casks/pcpm.rb` while inhomo stays a `Formula/`; the two coexist and `brew install xunull/tap/pcpm` works either way.

This costs nothing on Linux, contrary to the old "casks are macOS-only" rule of thumb: since Homebrew 6.x, portable stanzas such as `binary` work on both operating systems (Cask-Cookbook), and GoReleaser emits `on_linux` blocks accordingly. Because casks *do* apply `com.apple.quarantine` to what they download — unlike formulae, which Homebrew fetches with its own curl — and pcpm's binaries are unsigned (v0 does no Apple notarization, matching inhomo), the cask carries a `postflight` hook clearing that attribute.

## Consequences

- **Two projects in one tap deliberately use different release tooling.** That is a consequence of CGO, not an oversight or drift. Don't "unify" them by moving inhomo to GoReleaser unless its CGO constraint disappears — that is the reason the tooling differs, and it hasn't changed.
- **If pcpm ever gains a CGO dependency, revisit this.** Cross-compilation would stop working and a native runner matrix (inhomo's shape) would be required.
- CI runs `goreleaser check` and `goreleaser build --snapshot`, so a broken `.goreleaser.yaml` surfaces on pull requests instead of at tag time — a declarative config is easy to get subtly wrong and painful to fix once a tag is pushed.
- Publishing to the tap needs a token with `contents: write` on `xunull/homebrew-tap`; the repository's own `GITHUB_TOKEN` cannot write to another repository. The release job therefore carries two tokens with least privilege: `GITHUB_TOKEN` creates the Release, `TAP_PUSH_TOKEN` only pushes the cask. A missing `TAP_PUSH_TOKEN` fails the release rather than silently skipping — though only once publishing is reached, by which point the GitHub Release already exists, so configure the secret before the first tag.
