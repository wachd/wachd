# November 2026 validation: closeout summary

Closeout for issue [#92](https://github.com/wachd/wachd/issues/92) ("validation: Week 1 reproducible reliability & RCA lab, Nov 23–27, 2026"), coordinated in [#107](https://github.com/wachd/wachd/issues/107).

**Status: completed in a changed form, with several original steps not executed.** This is not a claim that every original acceptance criterion passed. Section 6 lists each original step and what happened to it.

This document is a sanitized summary. The raw evidence (run reports, redacted logs, harness code) lives in a private repository, `wachd-validation`. No lab credentials, private telemetry, personal data or raw logs are published here.

## 1. Headline results

| Area | Result | Where it was fixed |
|---|---|---|
| S2: authentication and tenant isolation | **14/14 PASS** on the final code. First runs: FAIL (12/14), exposing D11 and D12 | [#105](https://github.com/wachd/wachd/pull/105) |
| S1: worker crash and recovery | **16/16 PASS** on the final code. First run: FAIL (14/16), exposing D5a and D5b | [#105](https://github.com/wachd/wachd/pull/105) |
| Real-Claude RCA, final-code batch (16 runs) | **15 PASS, 1 provider timeout (BLOCKED), 0 completed-run FAIL** | |
| Harness self-tests | 118/118 PASS | |
| Go build, vet, tests against isolated PostgreSQL and Redis | PASS, 3 sequential runs, 0 skipped | |
| Release | image `v0.4.37` (Docker, amd64 and arm64) and Helm chart `0.4.40` published; one public-demo smoke test passed | [#105](https://github.com/wachd/wachd/pull/105), [#106](https://github.com/wachd/wachd/pull/106) |

Read the RCA line with section 3: earlier batches did have completed-run failures, which were investigated and fixed. "0 failures" describes only the final-code batch.

## 2. Environment and method

- **Where it ran:** one macOS (arm64) developer machine. Wachd server and worker were built from the checkout under test and run beside Podman-managed PostgreSQL and Redis containers, a local SMTP capture and, for non-AI scenarios, a mock AI backend. Not Docker Compose, and not `kind`.
- **One harness, no one-off scripts.** Every result comes from the same scenario runner (private repository). Each run records the product commit, a hash of any uncommitted changes, timestamps, redacted logs and per-assertion PASS/FAIL/BLOCKED/NOT RUN, with a failure class (product, AI provider, timeout, harness, environment).
- **Alert delivery was simulated.** Alerts were Grafana-format webhooks sent by the harness. **A real Grafana instance firing a real alert into Wachd was not exercised in this cycle.**
- **Notifications:** verified against a local SMTP capture. A real inbox was never read by automation.
- **Model:** RCA scenarios used `claude-sonnet-4-6`, Wachd's default Claude model, with a separately supplied validation key. The public demo is configured with `claude-haiku-4-5-20251001`.
- **Telemetry and code evidence for the RCA scenarios** came from a private external lab (a small checkout service with Prometheus, Loki and a private GitHub repository). It was read-only. Nothing was injected and no fault was triggered in the demo or in any shared or production system.
- **Ground truth was kept apart from Wachd.** The expected cause was held by the harness and never sent to Wachd or the model. An assertion checks that nothing the harness sent contains it.
- **Cost and tokens:** Wachd does not record provider token usage, so real token counts and cost were not measured.

## 3. Real-Claude RCA results

Two scenarios were repeated: a **historical regression** (a known `DB_POOL_MAX` 50→5 change in the lab's history, with real GitHub, Prometheus and Loki evidence) and a **negative control** (a real anomaly with no relevant pre-onset change; the correct answer names no commit and claims no deployment).

Final-code batch (8 runs of each, one batch):

| Scenario | Runs | PASS | FAIL | BLOCKED |
|---|---|---|---|---|
| Historical RCA | 8 | 8 | 0 | 0 |
| Negative control | 8 | 7 | 0 | 1 (provider timeout, run `…094723z-2ff0`) |

Run IDs (all `20261127t…`): negative control `094437z-50b2`, `094604z-d215`, `094723z-2ff0` (BLOCKED), `094948z-37d4`, `095110z-2cb4`, `095233z-7f2f`, `095355z-7c93`, `095518z-fad5`; historical `094525z-4bc4`, `094646z-dd5e`, `094908z-deb0`, `095033z-42fc`, `095155z-8b0a`, `095316z-f160`, `095439z-eea8`, `095600z-6302`.

What held in every completed final-code run:

- Historical: the verified causal commit and the pool-size reduction were named correctly (the commit in effect at that run's onset), with correct telemetry citations, no post-onset commit named, and no unrelated commit blamed.
- Negative control: no commit named as the cause, no deployment claimed, no post-onset commit offered, and a classification no stronger than the evidence allows.
- Evidence windows anchored to the alert onset, not to receipt time.

Earlier batches, kept in the private record, did have completed-run failures:

| Stage | Negative control | Historical | Causes found |
|---|---|---|---|
| Free-text parsing (pre-structured output) | 1 pass, 2 fail | 0 of 2 | model cited a metric that was never collected; classification stronger than the evidence; parsing dropped fields |
| Structured output, batch 1 | 4/5 | 0/5 | text-match checks counted history mentions as blame; a "check for a deployment" instruction counted as a claim |
| Structured output, batch 2 | 4/5 | 3/5 | same two causes |
| Structured output, batch 3 | 7/8 | 8/8 | one directive counted as a claim (harness) |

Three assertions were refined during the cycle, and the evidence is that all three were false positives on advice text, not on Wachd claims: (1) unrelated-commit blame is judged by the verified `causal_commit` field instead of any mention in the text; (2) conditional or directive remediation text ("check whether a recent deployment changed…") is not a deployment claim; (3) a metric-name verifier bug in Wachd itself (a trailing colon rejecting valid metrics) was a product fix, not a harness change. Assertions that test factual claims were not weakened. Post-onset commits named anywhere in the text still fail.

**Limits of these results.** The numbers describe one model, one lab, one incident shape and 16 runs, in a single sitting. They show consistent behaviour on those scenarios; they are not an accuracy benchmark and should not be quoted as one. The model always chose "probable" or lower, never "confirmed", so the confirmed path was exercised only by unit tests. The model sometimes over-narrates history ("fully verified"), which the checks do not catch. The final-code batch ran before the last small commits that were merged in #105 (the 120-second provider timeout and the cap that stops a commit alone confirming a deployment); those are covered by unit tests and CI, not by a repeated real-AI batch.

## 4. Defects found and shipped

All fixed in [#105](https://github.com/wachd/wachd/pull/105) (released as `v0.4.37`):

| ID | Defect | Found by |
|---|---|---|
| D1 | RCA used alert receipt time, not the alert's `startsAt` | historical RCA |
| D2 | error-rate metric window anchored to `time.Now()` | historical RCA, negative control |
| D3 | final analysis overwrote the preliminary timeline | negative control |
| D4 | AI answer parsing collapsed fields, or stored a malformed answer as a success | negative control |
| D5a | worker crash during analysis silently lost the investigation | S1 |
| D5b | Redis outage at ingestion silently lost the investigation | S1 |
| D11 | revoked or deleted users kept access through existing sessions | S2 |
| D12 | viewers could acknowledge, snooze, resolve and reopen incidents | S2 |

Also added in #105 from this work: verification of every commit, metric and log reference against collected data; a verified `causal_commit`; and a rule that a commit alone never confirms a deployment.

## 5. Release and demo smoke test

- **Image:** `ghcr.io/wachd/wachd:0.4.37` and `…/wachd-agent:0.4.37`, built by the repository's CI from the merge commit, with the Trivy HIGH/CRITICAL gate passing, amd64 and arm64.
- **Chart:** `wachd-0.4.40` (appVersion 0.4.37) via [#106](https://github.com/wachd/wachd/pull/106).
- **Public demo:** updated to the pinned v0.4.37 image and exercised once with one temporary session: provisioning, login, schema migration, one scenario, one persisted incident, investigation completed on the first attempt, one email processed to an authorised test recipient (delivery state only; the mailbox was not read), then the session was torn down.
- **What that smoke test is and is not.** The scenario's details (a fictional service, version and error rates) exist only in the alert text, so none of it is independent evidence. The result is useful for a different reason: Wachd rejected four unverifiable citations, lowered the classification to `INSUFFICIENT_EVIDENCE` with low confidence, and refused the "deployment cause" claim. That showed that the pipeline works on the demo's configured Haiku model, and it motivated the incident-page change below.

## 6. Original #92 steps: what happened to each

Status key: **Done**, **Changed** (done differently), **Partial**, **Not executed**, **Blocked**, **Superseded**.

| Original step | Status | Notes |
|---|---|---|
| Create `docs/validation/2026-11/` structure (PLAN, RUN_LOG, RESULTS, scenarios) in a docs-only PR | **Superseded** | Evidence is kept in a private repository; this file is the only public artefact. PLAN, RUN_LOG, RESULTS and public scenario files were not created |
| Record OS and tool versions, resources, SHA, deployment configuration | **Partial** | per-run reports record product commit, binary hash and environment for the local stack; a consolidated toolchain table was not written |
| Clean local **Docker Compose** baseline | **Changed** | Podman-based local stack instead of Compose |
| `go test ./...` | **Done** | with PostgreSQL and Redis, 3 sequential runs, 0 skipped |
| `make test-e2e` / `-tags integration` suite | **Not executed** | not run as such in this cycle |
| Frontend `npm run build` | **Done** | |
| Frontend `npm test` | **Not executed** as part of the cycle | frontend tests are run in the follow-up PR for this closeout |
| Document CI coverage vs integration tests | **Not executed** | CI runs lint, vet, unit tests and Helm render tests; DB-backed tests are not part of CI |
| Synthetic webhook → incident → queue → worker → analysis → notification | **Done** | via the harness; notification against a local SMTP capture |
| Timing, dropped and duplicated alert evidence | **Partial** | duplicates and drops covered by S1 and the baseline journey; end-to-end latency was not systematically recorded |
| Record model/backend; separate AI narrative from verified RCA | **Done** | model recorded per run; the verified-evidence layer in #105 makes the distinction explicit |
| Disposable **`kind` cluster**, sample workloads, observability | **Not executed** | replaced by the read-only external lab and a harness-owned local container lab |
| Safe failures: OOMKilled, bad deployment, optionally Redis outage | **Changed / Blocked** | OOMKilled and bad-deployment scenarios were **not run**. Redis unavailability **was** run (S1). A live fault-injection lab scenario was built but ended **BLOCKED**: Wachd's SSRF guard rejects local data sources (D6) and no commit evidence was approved for it |
| Capture ground truth and Wachd output per case | **Done** for executed scenarios | |
| Verify cleanup and reset | **Done** | each run verifies that nothing was left behind and nothing pre-existing was removed |
| Triage Loki selector issue #76; regression test for alternative labels | **Done** in the follow-up PR for this closeout | shipped after the validation cycle |
| Investigate `correlator.go`: commit is not a deployment | **Done** | addressed by the verifier in #105 and the UI wording in the follow-up PR |
| Investigate negative time-delta false correlation | **Partial** | addressed through onset anchoring and post-onset exclusion (D1, D2); the correlator function itself was not audited separately |
| Investigate short-SHA slicing safety | **Not executed** | not separately verified |
| Internal RESULTS.md with PASS/FAIL/BLOCKED/NOT RUN per scenario | **Changed** | kept in the private repository |
| Decide on a 30+ scenario benchmark in Week 2 | **Not decided** | no such benchmark exists; see limitations |

Exit gate:

1. *A third party can reproduce the environment and two fault scenarios from checked-in docs.* **Not met.** The harness and scenarios are private, and no public reproduction guide was written.
2. *Every executed scenario has a raw evidence path and a sanitized record.* **Met privately.**
3. *Unexecuted tests stay visibly NOT RUN; CI passes do not count as RCA accuracy.* **Met in this document.** Nothing here counts CI as RCA evidence.
4. *No demo changes and no new accuracy claims before the benchmark.* **Partly.** No fault was injected into the demo or any shared environment, and no accuracy claim is made. The demo **was** updated to v0.4.37 as an authorised release step.
5. *Reviewable PRs with explicit approval for merges.* **Met** for #105 and #106.

## 7. Remaining limitations

- No public, third-party-reproducible validation lab.
- No real Grafana-to-Wachd alert delivery test; no `kind` fault scenarios; no OOMKilled or bad-deployment scenarios.
- Real-AI results cover one model and one lab. Wachd records no provider token usage.
- `CONFIRMED_ROOT_CAUSE` is effectively unreachable today, because Wachd collects no deployment records.
- D6 (data sources on private or loopback addresses are blocked by the SSRF guard) is unchanged, so a fully local live-fault lab cannot be configured as a data source.
- The DB-backed Go tests are not run in CI, and run in parallel packages against one database they can collide (a pre-existing notification test); they pass with `-p 1`.
- The resolved-incident graph job is still Redis-only.

## 8. Evidence location

Private repository `wachd-validation`, branch `harness/scenario-runner` (latest local commit `8e72669` at the time of writing). Run IDs above refer to its `evidence/runs/` directory. Access is by request to the maintainers; the evidence is not published.

## 9. Proposed #92 comment and closing procedure

Post this on #92 **only after the pull request containing this document is merged**:

> **Closeout status for the November validation (completed in changed form; not all original criteria met)**
>
> The validation effort ran 26–27 Nov and its fixes shipped in v0.4.37 (#105, #106). Summary and per-step status: `docs/validation/2026-11/CLOSEOUT.md`.
>
> - Executed and passing on the final code: S2 14/14, S1 16/16, final real-Claude batch 15 pass / 1 provider timeout / 0 completed failures (earlier batches had failures that were fixed).
> - Changed: Podman-based local stack instead of Docker Compose; read-only external lab instead of a `kind` cluster.
> - **Not executed:** `kind` scenarios (OOMKilled, bad deployment), real Grafana alert delivery, `make test-e2e`, frontend tests during the cycle, the public docs structure.
> - **Not met:** the exit gate requiring a third party to reproduce the lab from checked-in documentation.
> - The raw evidence is private; no accuracy claims are made from it.
>
> Remaining work is listed in section 7 of the closeout. I propose closing this as **completed/superseded**, not as "all acceptance criteria passed". Please confirm whether the original owner of this issue accepts that framing.

Closing procedure: (1) merge the PR; (2) post the comment above; (3) close #92 as completed/superseded only if the original owner accepts that framing, otherwise leave it open with section 7 as the outstanding list; (4) close #76 if its acceptance criteria pass on `main`; (5) close #107.
