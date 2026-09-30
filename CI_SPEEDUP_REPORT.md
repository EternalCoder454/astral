# CI Speedup Report

Branch `ci-speedup`, from `beta` at 30e87a7 (Release 0.5.34). Every number is
marked **measured** (from `gh run list` / `gh run view` / the jobs API) or
**estimated**. Every claim is marked **evidence**, **inference** or
**speculation**.

## Baseline

gh works, so these are real run times.

Workflows and when they run (evidence, from the YAML):

| Workflow | Runs on | Notes |
|---|---|---|
| ci | push to beta, pull_request, dispatch | tests, race tests, staticcheck, in a prebuilt Fedora image |
| windows | push to beta (not docs), tags, dispatch | MSYS2 build, installer, attaches to the release on tags |
| android | push to beta (Android and phone page paths), tags, dispatch | APK, attaches on tags |
| flatpak | push to beta (not docs), tags, dispatch | new in 0.5.34 |
| ci-image | push to beta when the Dockerfile or go.mod/go.sum change | builds and pushes the CI image |

Total time per workflow, wall clock from start to last update (measured):

| Workflow | Beta pushes | Release tags |
|---|---|---|
| ci | 150, 151, 151, 155s (median 151s); 159s dispatched on this branch | does not run |
| windows | 243, 249, 258s (median 249s) | 1134, 1335, 1366s (median 1335s) |
| android | 95, 138, 148s (median 138s) | 98, 105, 169s (median 105s) |
| flatpak | 1129s (the one run there has been, 0.5.34) | 1095s (v0.5.34) |
| ci-image | 1851, 1877, 1949, 2161s (only when the recipe changes) | does not run |

The v0.5.34 tag, pushed during this work on the unchanged workflows, took
windows 1041s, android 168s and flatpak 1095s (measured).

Slowest jobs (measured): windows `build` on a tag (1335s), ci-image `build`
(about 1900s, rare), windows `build` on beta (249s), ci `test` (145 to 153s),
android `build` (95 to 169s).

Slowest steps (measured):

1. windows Build on a tag: 1106s (v0.5.33), against 26s on beta. A cold gotk4 compile.
2. windows Set up MSYS2: 117 to 120s on every run.
3. ci Test under the race detector: 59 to 68s.
4. ci Initialize containers (pulling the CI image): 44 to 60s, in each of the two jobs.
5. windows Gather the runtime, Build the installer: 22 to 26s each.

Why tags build cold (evidence plus inference): the default branch is
`release` (`gh repo view`), a tag run can only restore caches made on the
default branch, and no workflow runs on `release`. The windows cache step on a
tag found nothing it could use, and Build took 1106s.

## What happened

The user allowed the release and deploy workflows to be changed after the
first measurements, since the biggest costs were there. Runner types were
not touched.

| # | Change | File | Before | After | Kept |
|---|---|---|---|---|---|
| 1 | Race detector as its own job, beside the tests | ci.yml | 158, 159s (median 158.5s) | 119, 125s (median 122s) | yes, measured |
| 2 | Tag reuses beta's installer for the same commit | windows.yml | tags 1041, 1134, 1335, 1366s (median 1235s) | 31, 24s (median 27.5s) | yes, measured by dispatch with the reuse input |
| 3 | Tag reuses beta's APK | android.yml | tags 98, 105, 168, 169s (median 136.5s) | 18, 12s (median 15s) | yes, measured the same way |
| 4 | Race detector split into two shards | ci.yml | median 122s | 126, 132s (median 129s) | no, reverted: slower |
| 5 | Tag reuses beta's Flatpak bundle | flatpak.yml | tag 1095s | 24, 23s (median 23.5s) | yes, measured the same way |
| 6 | Flatpak keeps its dependency build between runs | flatpak.yml, packaging/flatpak/io.github.astral.yml | 1129s | 195, 182s (median 188.5s) after one cold run of 1120s that filled the cache | yes, measured |
| 7 | ci skips a push that changes only top-level docs | ci.yml | | | yes, unverified (a dispatch ignores path filters; the filter was checked by script, see below) |
| 8 | ci checks formatting before building | ci.yml | median 122s | 127, 120s (median 123.5s) with 7 and 9 | yes, measured: no change on a passing run, as expected |
| 9 | Every job has a timeout | all five | | | yes, no effect on a passing run |
| 10 | Beta writes the Gradle cache | android.yml | beta 95, 138, 148s | 120 (filling), 125, 165s; the restored cache stayed 432 KB | no, reverted: not faster |

Details (evidence unless marked):

1. The test job ran the race detector last, 59 to 68s of 145 to 153s. It
   depends on nothing before it. Now ci's slowest job is race at 115 to 121s.
2. The default branch is `release` and a tag run can only restore caches from
   it; nothing runs there, so every tag compiled gotk4 cold (Build 1106s on
   v0.5.33, 1039s on a dispatch here). Beta always builds a release commit
   minutes before its tag. The tag run now finds that run
   (`gh run list --branch beta --commit`) and downloads its artifact,
   skipping every build step; it builds as before when there is none. Both
   paths were run: reuse (runs 36784842701, 36784926642) and build (run
   36784978164, success, 1293s cold). Needs `actions: read`.
3. The same for the APK (runs 36784980071, 36785019812).
4. Each shard still spent 47 to 57s in its race step (compiling and linking
   race test binaries, inference) and a fourth container pull added 36 to 68s
   of start. Reverted with `git revert`.
5. The same pattern as 2, with the lookup in a job of its own on the plain
   runner, since the Flatpak builder image need not have gh (inference; not
   checked). Runs 36785413198, 36785464363; the downloaded bundle is the same
   7177177 bytes as beta's.
6. The flatpak-builder action restores only cache keys starting
   `flatpak-builder-x86_64` and appends the arch (evidence, its index.js at
   v6). The workflow keyed its cache on the commit, so nothing was ever
   restored, and one module compiled everything from the whole tree. Now the
   dependencies are a module of their own built from go.mod, go.sum and an
   import list, into a Go cache the Astral module builds from and deletes,
   and the key is go.sum plus the manifest. Runs 36785711478 (cold: the
   dependency module took 15.5 minutes, Astral 12s), 36787495321 and
   36787804750 (warm: "Cache hit for golang, skipping build", "Cache hit for
   astral-deps, skipping build", Astral 13s). The bundles are 7175893 to
   7177575 bytes, the same as before, so the build cache is not packaged.
7. Top-level Markdown is read by no test; `WHATSNEW.md` is (TestShippedNotes),
   and so is Markdown under internal and assets (TestNoDashesInTheProject), so
   those still run.
8. Saves time only when formatting fails (inference).
9. The default is six hours.
10. setup-gradle writes its cache only from the default branch unless told
    otherwise (evidence: `cache-read-only: true` in the log). Letting beta
    write it did not put Gradle's dependencies in it: the restored cache
    stayed about 432 KB, and the builds were no faster (runs 36788171065,
    36788367150, 36788561638). Why the dependencies are not cached was not
    found (unknown). Reverted with `git revert`.

## What it means

Measured, per run:

* ci, every beta push: 158.5s to 122s median (23% less).
* A release tag: windows about 1235s median to 27.5s, android 136.5s to 15s,
  flatpak 1095s to 23.5s. The slowest tag job, which is what a release waits
  on before it can be published, goes from about 18 to 22 minutes to about
  half a minute.
* windows and android on beta pushes: unchanged (not touched).
* flatpak on beta pushes: 1129s to 188.5s median (83% less), whenever go.sum
  and the manifest are unchanged; a change to either costs one cold run.

Totals, adding up the workflows that run (medians, measured per workflow;
the totals are arithmetic on those):

| Case | Before | After | Saved |
|---|---|---|---|
| A code push to beta: ci + windows + flatpak (android only when its paths change) | 158.5 + 249 + 1129 = 1536.5s of runner time; the last to finish, flatpak, at about 1129s | 122 + 249 + 188.5 = 559.5s; the last to finish, windows, at about 249s | 64% of runner time; the wait for every check drops from about 19 to about 4 minutes |
| A release tag: windows + android + flatpak | 1235 + 136.5 + 1095 = 2466.5s; the release could be published after about 1235s | 27.5 + 15 + 23.5 = 66s; after about 27.5s | 97% |

The tag figures after the change come from dispatching each workflow with
the reuse input, which takes the same steps a tag does except attaching to
the release. No real tag has run on the new workflows yet.

## What is risky

* Tag reuse (2, 3, 5) ships the artifact beta built for the same commit,
  rather than one built on the tag. Same commit, same workflow file, so the
  same build (inference); the version comes from version.go either way. If
  beta's run for that commit was cancelled, failed or skipped by a path
  filter, the tag builds as before, so the worst case is the old time.
  Artifacts are kept 90 days, and a tag follows its beta run by minutes.
* Tag reuse needs `actions: read` in three workflows' permissions blocks,
  read only, to list runs and download another run's artifact. It was the
  one permissions change; the user allowed changing these workflows.
* The tag path's release steps (notes, attach) were not run: they only run
  on a tag. The notes step in windows.yml now runs in bash instead of the
  MSYS2 shell, since MSYS2 is not set up when reusing; awk and sed are in the
  runner's Git bash (inference, not tested). The first release after merging
  is the real test: check its draft has all three assets and the notes.
* The docs filter (7) could hide a failure only if a test starts reading a
  top-level Markdown file other than WHATSNEW.md. None does now (evidence:
  grep of the tests).
* The Flatpak dependency module (6) lists the packages to warm by hand. One
  added to Astral later is compiled with Astral instead, which is slower but
  not wrong. The build cache is deleted before the app is packaged.
* Changing ci-image.yml (the timeout) makes the CI image rebuild once when
  this lands on beta, about 30 minutes, since that workflow watches its own
  file.
* A flaky test turned up: TestLetThemTalkFromThePhone failed once (run
  36785048833) on code identical to beta, where it passes. Not caused by
  these changes (evidence: the Go code is unchanged); flagged as a separate
  task.

## Skipped items

* Larger runners or self-hosted ones: not changed, as instructed. A larger
  Windows runner would shorten MSYS2 setup (117 to 132s, mostly unpacking
  about a hundred packages) and the beta build; a self-hosted runner with
  MSYS2 installed would remove the setup entirely (speculation).
* MSYS2's Go replaced by the runner's cached Go: saves about 20s of setup
  (evidence, install log) but needs MSYS2 to inherit the Windows PATH, which
  risks a different toolchain being found. Not worth it for 8%.
* Inno Setup compression: 25s of the Windows build, but a lower setting
  makes the installer people download bigger.
* The CI image pull (36 to 68s per ci job): shrinking the image means
  rebuilding and pushing it, and its caches are what make the jobs fast.
* push and pull_request duplication: this repository does not use pull
  requests; ci's pull_request trigger was left as it is.
* concurrency: every workflow already cancels superseded runs, on beta as
  well; the instruction to cancel only on pull requests was read as not
  adding more, and the existing groups were left alone.
* Matrix, trimming a matrix, nightly integration tests, QEMU: none apply.
  Live model tests already stay out of CI through -short.
* Sharding the race detector further: tried (4) and reverted.

## Final reason

Stop condition 2, nothing left: every item on the list either has been
done, was tried and reverted (4 and 10), or does not apply or is not safe
here (see Skipped items). Condition 1 did not trigger: of the last kept
changes, 8 and 9 each cut less than 5% (measured, no change), but 7 is
unverified and does not count, and 6 before them cut 83%. 11 commits were
made, under the cap of 15.

* Starting total (measured): 1536.5s of runner time for a code push to
  beta, 2466.5s for a release tag.
* Ending total (measured, the tag case on the reuse path by dispatch):
  559.5s for a push, 66s for a tag.
* Saved: 64% for a push, 97% for a tag.

## How to verify

```bash
# the helper used here: dispatch ci.yml on this branch N times, one after the
# other (the workflow cancels a superseded run), and print wall and job times
scratchpad/cirun.sh 2 label

# a workflow's tag path, without a tag: reuse a commit beta has built
gh workflow run windows.yml --ref ci-speedup -f reuse=<sha built on beta>
gh workflow run flatpak.yml --ref ci-speedup -f reuse=<sha built on beta>

# after merging, the first release is the real test: its three tag runs
# should each finish in well under a minute, and the draft should carry the
# installer, the APK, the Flatpak and the notes
gh run list --branch v<version> --json workflowName,databaseId,createdAt,updatedAt

# or by hand
gh workflow run ci.yml --ref ci-speedup
gh run list --workflow ci.yml --branch ci-speedup --limit 3
gh run view <id> --json jobs --jq '.jobs[] | "\(.name) \(.startedAt) \(.completedAt)"'
```
