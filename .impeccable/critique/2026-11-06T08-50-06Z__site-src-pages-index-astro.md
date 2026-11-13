---
target: the landing page
total_score: 25
max_score: 32
na_heuristics: 7,10
p0_count: 0
p1_count: 0
target_identity: "file:site/src/pages/index.astro"
target_fingerprint: "sha256:231a10e91ff2a7d5ff5b053ddd12c303dd4fe2ee28b63341982d96c4e110a179"
target_path: site/src/pages/index.astro
timestamp: 2026-11-06T08-50-06Z
slug: site-src-pages-index-astro
---
Method: dual-agent (A: gentle-ai-explore-local with rendered screenshots · B: gpt-luna-6)

# Design Health Score — 25/32 (78.1%, Good)

| # | Heuristic | Score | Key issue |
|---|---|---:|---|
| 1 | Visibility of system status | 4/4 | Alpha, available controls, unfinished work, limitations and the expected local-start state are explicitly distinguished |
| 2 | Match system / real world | 3/4 | Clearer opening and inline ACL explanation; "MVP", "recent MFA" and "Task" still assume familiarity |
| 3 | User control and freedom | 3/4 | Direct quickstart anchor, optional GitHub route, `task down`; the local address is inert code rather than a link |
| 4 | Consistency and standards | 3/4 | Cohesive typography, surfaces and action hierarchy; misleading feature-card hover removed |
| 5 | Error prevention | 3/4 | Recent-MFA requirement, development-versus-production distinction, password warnings |
| 6 | Recognition rather than recall | 3/4 | Real screens, task grouping, explicit availability headings, success checkpoint; screenshot details too small |
| 7 | Flexibility and efficiency | n/a | Persuade surface |
| 8 | Aesthetic and minimalist design | 3/4 | Strong hierarchy and restrained console language; availability paragraphs and task cards still repeat information |
| 9 | Error recognition and recovery | 3/4 | Two named failure branches with commands and next steps; checkpoint's fixed `admin` conflicted with the configurable-username branch (fixed in 94d555d) |
| 10 | Help and documentation | n/a | Excluded for like-for-like Persuade scoring |

Baseline: 20/32, Acceptable → Current: 25/32, Good. Same rubric, same denominator, same exclusions.

## Baseline comparison

| Baseline priority | Status | Evidence |
|---|---|---|
| P1 #1 Availability contradiction | FIXED | "Available controls" lists listeners, TLS, settings, bridges, plugins and the root configuration editor; "Remaining MVP work" separates incomplete coverage; recent-MFA sits beside availability. site/src/pages/index.astro:68-77 |
| P1 #2 Primary CTA sends visitors away | FIXED | The cyan action is "Try locally", linking to #quick-start; GitHub is secondary. index.astro:52-57 |
| P2 #3 No product evidence | FIXED | Two authentic captures, and later a third: the deploy panel with its preview controls and deployment history. The stack presentation became a collapsed disclosure. |
| P2 #4 Insider language | PARTIALLY FIXED | "Administer an existing Mosquitto broker from the web"; ACLs explained as rules over topics each user can access. "MVP", "recent MFA" and the prerequisite "Task" remain unexplained. |
| P2 #5 Quickstart without success checkpoint | FIXED | "Success checkpoint", "No bootstrap credentials in the logs?", "Dashboard unreachable?" and `task down`. |

## New issues introduced

- [P2] Product evidence present but not sufficiently inspectable — addressed by the deploy capture as full-width evidence plus full-size links to every capture.
- [P2] The new availability layer creates a duplicated reading pass — addressed by replacing the repeated capability cards with the checks the captures illustrate.
- [P2] The checkpoint assumed a username that the troubleshooting branch allows to change — fixed in commit 94d555d.

No P0 or P1 regression established by this review.

## Strengths

1. Honesty is now structural: availability, unfinished work and limitations are separate, prominent destinations.
2. The action hierarchy serves the product goal: "Try locally" leads to a bounded development trial.
3. The onboarding ends with reassurance and recovery.

## Minor observations

- Noninteractive feature cards no longer advertise hover.
- Font files are imported, so the declared fonts load.
- "Image CI workflow" is more accurate than a status-like CI badge.
- The Astro dev toolbar seen in the review screenshots is a dev server artifact and is absent from the production build.
- The production-documentation link is underlined only on hover.

## Questions to consider

1. What should earn the first major visual moment: the availability map, a legible review/apply capture, or both paired?
2. Should "first success" mean seeing sign-in, reaching an authenticated dashboard, or confirming a connected broker?
3. Could the remaining task cards become one concrete configuration-change walkthrough?
