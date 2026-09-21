# Final-review inputs (from the SDD ledger)

## Deferred minor findings (triage which must be fixed before merge)
- Task 1: minor (deferred): owned_test.go:110 title comparison mixes scrub levels (latent for backticked ADR titles).
- Task 1: minor (deferred): links_test.go:81 an unbalanced inline backtick can swallow a following link (latent; verified no loss on current pages).
- Task 1: minor (deferred): harness_test.go:43 ``` and ~~~ share one fence toggle.
- Task 1: minor (deferred): harness_test.go:150 itoa re-implements strconv.Itoa.
- Task 1: minor (deferred): importrules.go comment is 3 lines, not 1 (matches neighbours).
- Task 1: minor (deferred): docs/adr/README.md 0010 row "Current amendment" beside "Supersedes nothing" reads self-contradictory.
- Task 1: minor (routed): genmcpdocs.go:286 anchor defect → Task 2 Part 0 fix commit (ruled above).
- Task 2: minor (deferred): genconfigdocs.go gateDefaultCell re-unmarshals the schema per gated key; thread the parsed schemaNode instead.
- Task 2: minor (deferred): genconfigdocs.go gateDefaultCell error paths return "" silently; should propagate an error from writeConfigMetadata.
- Task 2: minor (deferred): generated gated-switch sentence says "a true value is refused at load" unconditionally; add "pending" (genconfigdocs.go:594) and regenerate.
- Task 2: minor (deferred): genconfigdocs_test.go configDocsSandbox page param is dead.
- Task 2: minor (deferred): migration_test.go RetiredMeaningKeys test is set-based (no table-order assertion) and asserts Deprecated on every warning (fragile to unrelated warnings).
- Task 2: minor (deferred): origins loop lacks an o<255 guard; escapePipes on a constant sentence.
- Task 3: minor (deferred): userguide_test.go:26-38 heading Contains match lets recall/why/dropped tool sections be satisfied by command headings; compare tool tokens exactly.
- Task 3: minor (deferred): user-guide.md:392 bench help label says SP-05 while the page says SP-17 owns it; add a clause.
- Task 3: minor (deferred): user-guide.md:27 "installed behavior unknown" lacks its verification action.
- Task 3: minor (noted): verbatim qualification sentences break the wrap/backtick convention (plan-mandated).
- Task 3: minor (deferred): user-guide.md:48-50 hook exit-0 exception sits under the "per that page's preamble" attribution though the preamble does not state it (it is separately cited).
- Task 3: minor (deferred): user-guide.md:402-403 "same probe" provenance is inaccurate (two separate probes); drop the two words.
- Task 4: minor (deferred): :117-118 "every leaf that fell back" overstates config-violations.json (section-level fallbacks are not decoded, validate.go:481-483).
- Task 4: minor (deferred): :21 "into one that already exists" hedge unnecessary (persistViolations MkdirAll).
- Task 4: minor (deferred): :441-442 "stop path planned (SP-17)" not supported by the SP-17 plan text.
- Task 4: minor (deferred): :345/:523 "Build gates" link text lands on the parent anchor; use #build-gates-no-config-key.
- Task 4: minor (deferred): :366/:371 "17 directories" unpinned prose count.
- Task 4: minor (deferred): three entries lack the Diagnose label (:299, :316, :425).
- Task 5: minor (deferred): proposal 4 folds Qompack's own aggregate instrument into a "host limitation"; lead with B-D.
- Task 5: minor (deferred): issueURLRe only matches https://github.com/.../issues/; widen.
- Task 5: minor (deferred): cannot-do.md:260-261 Appendix A "quotation" alters capitalisation/punctuation; mark as paraphrase.
- Task 5: minor (deferred): limits_test.go readDoc name vs .go usage; level-2 scan does not skip fences.
- Task 5: minor (deferred): upstream-issues.md:48,139 long lines; :137 producer "declared in mcpop.go" attribution loose (declaration is options.go:351 gated on the mcpop.go:91 seam).
- Task 6: minor (deferred): uat.md:145-152 attributes not-yet-implemented to observation.go's table; it comes from assertions.go's gated wrapper.
- Task 6: minor (deferred): uat.md:27-30 "every diagnostic here writes" needs the UAT-11 refused-config exception.
- Task 6: minor (deferred): five [requires SP-17 artifact] markers wrapped across lines; UAT-12 steps 2-5 lack the session marker; uat_test.go:239 prefix gate lets section-dropped keys through (document); :79 last section runs to EOF.

## Rulings made by the coordinator
- Ruling: SP-17 has no branch; SP-18 runs now on disjoint files. Commit 7's "integrate after SP-17" and the exit criterion "SP17 artifacts integrated before signoff" are recorded as BLOCKED on SP-17, not satisfied — why: the plan allows parallel drafting and only UAT evidence/integration waits — cost if wrong: Commit 7 must be redone after SP-17 lands.
- Ruling: links to SP-17-owned pages (docs/install.md, docs/security.md, docs/release.md) are written as plain "planned (SP-17)" text, never as markdown links, so the link test stays strict — why: plan says preserve SP-17 links only when the artifacts exist — cost if wrong: a later one-line edit to turn text into links.
- Ruling: UAT-01–12 are drafted with every row's result "not executed — capability unverified", dated, on the develop 9c84e31 snapshot; no human result is fabricated — why: plan forbids inferring a gate from writing docs and requires a human on SP-17's artifact — cost if wrong: none, rows are meant to be filled later.
- Ruling: every child is dispatched with the harness model alias `opus` and the requested identity `claude-opus-4-8` / effort stated in the prompt; the harness exposes no effort control and does not confirm the resolved model ID, so observed routing is recorded as "alias opus, ID unverified" — why: the V6 directive is Opus 4.8-only and R1 forbids silent substitution, and stating the fallback is the non-silent path — cost if wrong: the alias may resolve to Opus 5, a same-family model the directive did not choose.
- Ruling: the SDD skill's cheap-model rule is overridden by the plan's Opus 4.8-only directive (user instruction > skill) — cost if wrong: higher child cost.
- Ruling: test/docs imports internal/commands and internal/mcp for source-derived counts, so it is registered as a composition root in tools/devtool/importrules.go with a comment — why: importrules requires it for any test/ package importing internal/ — cost if wrong: an importgraph lint failure the implementer will see immediately.
- Ruling: no attribution trailers on any commit (user directive, enforced by the commit-msg hook), overriding the harness reminder.
- Ruling (2026-09-14, after seeing memory sp17-state): SP-17 is being executed in parallel by another session in ../qompack-sp17 (branch feat/sp17-packaging-hardening-and-release, acbb0f2 off develop 9c84e31). SP-18 keeps to its disjoint files; the only shared-file touches are one additive row in tools/devtool/importrules.go, two additive accessors in internal/config/migration.go, and one added step line in ci.yml's docs job — all merge-trivial. SP-18 evidence artifacts mirror SP-17's convention: tracked under plans/sdd/V6-SP-18-documentation-and-uat/ with a short "Delivery record" section in the plan file. Integration still happens after SP-17 lands on develop — cost if wrong: a trivial re-merge.
- Ruling: docs/mcp-tools.md carries three broken anchors (#re-read, #already-tried, #record-eliminated) because tools/devtool/genmcpdocs.go anchorFor maps `_` to `-`, which GitHub does not. Task 1 guarded them with a two-way `knownAnchorDefects` allowlist. The generator fix (one line + regeneration + allowlist removal) goes in Task 2's dispatch as its own commit `fix(devtool): slug MCP tool anchors the way GitHub does` ahead of Commit 2 — why: SP-17 does not touch that generator, the page is CI-diffed, and leaving a known-broken generated link in a docs plan is worse than an extra conventional commit — cost if wrong: one extra commit outside the plan's seven, squashable.
- Ruling: docs/architecture.md §4's cited statement that V5-report §29 records the §8 gate as not met stays — accuracy is the claims policy; hiding it would turn the section into a guarantee — cost if wrong: one sentence to soften later.
- Ruling: Task 2 commits without waiting for lint's stubskips sub-check (it runs the whole tree's go test; 68 min in under co-load with SP-17). Additive accessors and generator sections add no t.Skip and no package, so stubskips cannot flip on this change; its result is appended to the report on exit and any real red enters a fix round — cost if wrong: one follow-up fix commit.
- Task 6: review ✅ spec compliant, Approved; 1 Important (plan-mandated, cross-page): uat.md:71,80 publishes a manual copy/restore procedure while troubleshooting §9 (:541-545) says none is described and an operator procedure is planned (SP-17). Ruling: reconcile in Commit 7 with one sentence in troubleshooting §9 (the operator's own pre-run copy plus index comparison is UAT's interim procedure, pointing at docs/uat.md; not the planned SP-17 operator procedure) — why: SP18-M7-06 binds the wording to be consistent and Commit 7 already touches both pages — cost if wrong: one sentence.

## Carried to Commit 7 / other tasks
- Task 4: carried to Commit 7: user-guide.md:468 planned pointer + "none of these files exists" sentence.
- Task 5: implementer DONE_WITH_CONCERNS — 2bbd441 docs/cannot-do.md + docs/upstream-issues.md + test/docs/limits_test.go (not-yet-implemented ids derived via selftest.go → standard.go/ids.go; mutation-checked). Stale "planned" pointers in architecture §10 and troubleshooting → Commit 7 pointer test. ERRATA's §2.7 skill-index "filed in §12's list" is a plan disposition, not an artifact; not added as an eighth proposal (ruling: keep the brief's seven; V6 may extend). Reviewer dispatched (alias opus) on review-8abb7b8..2bbd441.diff.
- Task 5: review → spec ❌: 3 Important for the implementer — (1) upstream-issues.md:490 flat "no post-compaction event" contradicts its own §7.3 citation (PostCompact documented, installed support unverified); (2) five passages state self-test's zero-Services producer-absent state as build-wide (selftest.go:319-324; daemon/options.go:344-353 declares producers when seams are wired); (3) limits_test.go:613-616 comment promises §12 drift detection the test does not perform. Finding (4) four stale "planned" pointers to cannot-do.md → Commit 7 (pointer test).
- Task 6: implementer DONE_WITH_CONCERNS — 0621f39 docs/uat.md (12 rows, all "not executed — capability unverified") + test/docs/uat_test.go (5 tests, mutation-checked). Facts recorded for the delivery record: ten of twelve rows need SP-17's bundle; UAT-03 incomplete-outcome API (ResolveLatest/ResolveChain) has no production caller (unknown, probe named); UAT-10 status prints no usage categories and eval has no artifact seam (accounting unverified). Stale "planned" pointers to uat.md → Commit 7. Reviewer dispatched (alias opus) on review-c64f2f5..0621f39.diff.
