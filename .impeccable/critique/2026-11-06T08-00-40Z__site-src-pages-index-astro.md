---
target: the landing page
total_score: 20
max_score: 32
na_heuristics: 7,10
p0_count: 0
p1_count: 2
target_identity: "file:site/src/pages/index.astro"
target_fingerprint: "sha256:a1ebe034d91559b9dd124d10d6774884e5d31c9710b4092f8d576ebc8acb18d3"
target_path: site/src/pages/index.astro
timestamp: 2026-11-06T08-00-40Z
slug: site-src-pages-index-astro
---
Method: dual-agent (A: gentle-ai-explore-local · B: gpt-luna-6)

# Design Health Score — 20/32 (62.5%, Acceptable)

| # | Heuristic | Score | Key issue |
|---|---|---:|---|
| 1 | Visibility of system status | 2/4 | Alpha conspicuous, but available-vs-planned claims contradict product truth |
| 2 | Match system / real world | 2/4 | "ACLs", "bootstrap admin", "Task" assume insider knowledge |
| 3 | User control and freedom | 3/4 | No forced funnels; no direct jump to the local trial |
| 4 | Consistency and standards | 3/4 | Consistent components; noninteractive cards advertise hover |
| 5 | Error prevention | 3/4 | Useful guardrails; recent-MFA requirement for apply absent |
| 6 | Recognition rather than recall | 2/4 | Interface never shown; visitor assembles capabilities mentally |
| 7 | Flexibility and efficiency | n/a | Persuade surface |
| 8 | Aesthetic and minimalist design | 3/4 | Coherent hierarchy weakened by 13 repetitive cards and over-prominent stack |
| 9 | Error recognition and recovery | 2/4 | Trial has no failure branch (missing credentials, unreachable dashboard) |
| 10 | Help and documentation | n/a | Persuade surface |

## Design specificity

Product-specific voice inside a largely interchangeable developer-tool composition. One-broker boundary, inspection-not-enforcement language, amber warning and numbered quickstart belong to this product; radial blue background, pill badges, translucent cards are category-generic. "The Control Room" is present in styling, not yet in the organizing idea.

Deterministic scan (B): source clean (exit 0, zero findings across the 62 slop rules) — problems are content and IA, not slop tells. URL scan unavailable: engine cannot attach to host Chromium (Chrome 148 launches but never exposes the WS endpoint the engine waits for; recorded). Source-only scan cannot see rendered contrast or runtime text size.

## What's working
1. "Observed MQTT traffic" and "Optional message diagnostics" cards draw unusually responsible boundaries — preserve this precision.
2. "Before relying on production changes" is a meaningful caution component with inspectable links.
3. The 4-step Quick start is the strongest expression of "The Control Room".

## Priority issues
1. [P1] Availability map contradicts product truth: page sells listeners/TLS/bridges/settings/plugins/config editing as planned; README and PRODUCT.md list them available today. Fix: one compact availability map (available controls / remaining MVP work / known limitations), MFA-recent stated beside apply claims. (/impeccable clarify)
2. [P1] Primary CTA sends visitors away: "View on GitHub" is the cyan action; no "Try locally" anchor to the quickstart. Fix: "Try locally" primary, GitHub secondary, API spec to developer resources. (/impeccable layout)
3. [P2] Console aesthetic with no product evidence: 13 cards + large cyan stack panel, no authentic capture or concrete task example. Fix: authentic annotated capture (or factual diagram, never fabricated UI), demote stack, group cards by task. (/impeccable distill)
4. [P2] Opening calibrated to insiders: "MQTT users, basic ACLs and observed traffic" before understandable outcomes. Fix: accessible first layer (ACL = rules over which topics a user can access), precise terminology below. (/impeccable clarify)
5. [P2] Quick start ends at an address, not a confirmed success: no expected first screen, no troubleshooting, no stop instructions. (/impeccable onboard)

## Persona red flags
- Jordan (first-timer): "ACLs" unexplained; dominant CTA exits the landing; "Task"/"bootstrap admin" without setup links (hero status, primary CTA, quickstart steps 03-04).
- Riley (stress tester): planned-vs-available contradiction turns the trust mechanism into evidence of inconsistency.
- Casey (mobile): single-column card run before quickstart, no "Try locally" jump, compact touch targets (source-indicated, not browser-tested).

## Minor observations
- Hover on noninteractive feature cards (FeatureSection.astro:18).
- Manrope / JetBrains Mono declared but never loaded (no <link>, no @font-face) — site renders on system fallbacks.
- "readdocs/production.md" — missing space before link (index.astro:211).
- "Image CI" badge is a workflow link, not a live status.
- Token drift already documented in DESIGN.md.

## Questions to consider
1. Should the hero promise broad configuration — or precisely answer "what can I safely change today?"
2. Which deserves the strongest visual surface: the eight-part stack, or one authentic demonstration of reviewing a broker change?
3. Should the page end on what is unfinished — or on recognizing a successful, bounded first local session?
