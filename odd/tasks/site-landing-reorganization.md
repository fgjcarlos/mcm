# Site landing reorganization (Impeccable)

Reorganize the public landing (`site/`) applying the Impeccable critique baseline
(`.impeccable/critique/2026-11-06T08-00-40Z__site-src-pages-index-astro.md`,
20/32) with the user-selected priorities. Design system of record:
`DESIGN.md` ("The Control Room"). Product truth: `PRODUCT.md` and
`docs/product-scope.md`.

## Specs

- S1 (priority #1, user: "Verdad de alcance"): reconcile the page's
  availability claims with `README.md`'s capability table and
  `docs/product-scope.md`: listeners, broker TLS, general settings, bridges,
  plugins (incl. Dynamic Security) and the advanced root `mosquitto.conf`
  editor are available today, not planned. Publish one compact availability
  map: available controls / remaining MVP work / known limitations. State the
  recent-MFA requirement beside apply claims. What stays planned/deferred:
  full backup/restore (#295), partial native ACL coverage, explicit version
  support matrix, fleets/multi-broker/SSO/HA/PostgreSQL.
- S2 (user: "Camino al run local" — second P1): primary CTA becomes
  "Try locally" anchoring to the Quick start section; GitHub secondary;
  OpenAPI spec moves out of the hero action row.
- S3 (user: "Captura real del HMI"): integrate a real screenshot of the
  running HMI (assets provided by parent, taken from the live local stack;
  never fabricated) with a caption stating its real scope.
- S4 (clarify, accessible layer): explain ACLs on first use ("rules that
  control which topics each user can access"), observed traffic as "messages
  MCM sees arrive, not a complete client inventory"; plain-language sentence
  that MCM is a self-hosted administration interface for an existing
  Mosquitto broker. Precise terminology stays, below the orientation layer.
- S5 (onboard): Quick start gains a success checkpoint ("you should now
  see…"), failure branches (no bootstrap credentials in logs; dashboard
  unreachable) and how to stop the stack (`task down`).
- S6 (distill): group the 13 feature cards task-oriented; demote the stack
  panel; the strongest visual surface belongs to the real product evidence.
- S7 (polish, minors): load Manrope + JetBrains Mono for real (parent
  installs `@fontsource` packages; worker imports the CSS); remove hover
  feedback from noninteractive cards; fix the `read` + link spacing; "Image
  CI" no longer styled as a status badge; unify `@theme` tokens with the
  operative palette per `DESIGN.md`'s drift note.

## Tasks

| ID | Specs | Route | Commit |
|----|-------|-------|--------|
| T1 | S1, S4 | gpt-luna-6 (site/src/**) | 26ec8e7 |
| T2 | S2, S5 | gpt-luna-6 (site/src/**) | 26ec8e7 |
| T3 | S3, S6 | gpt-luna-6 (site/src/**, assets) | 26ec8e7 |
| T4 | S7 | gpt-luna-6 (site/src/**) | 26ec8e7 |
| T5 | verify: build, detector, screenshots, re-critique | parent | 26ec8e7, 43989af (verified 20/32 -> 25/32) |
| T6 | issue + branch + PR | parent (user decides) | pending |

Delivery slice boundary: three reviewable units landed on
`feat/site-landing-reorganization` (299, 358 and 74 authored lines; the
cumulative counter exceeds the ~400 advisory because the units are separate
review surfaces: behavior, design context, repository hygiene).

## Log

- L1: "Ahora necesitamos organizar mejor la web, esto supone cambios, instala
  este repo https://github.com/pbakaus/impeccable y vamos a aplicarlo"
  (verbatim).
- L2: Interview answers: visitor = "cualquier usuario que quiera entrar";
  landing success = both balanced (honest scope + fast local run); brand
  bindings = current name and tone. North Star = "The Control Room".
- L3: Critique decisions: priority = availability truth first; scope = full
  backlog (P1+P2+minors); evidence = real HMI capture, never fabricated.
- L4: Re-critique (dual-agent) measured 25/32 vs the 20/32 baseline: both P1
  fixed, P2#3 fixed, P2#4 partially fixed, P2#5 fixed; three new P2s (capture
  scale, duplicated inventory, the hardcoded `admin` checkpoint) and no P0/P1
  regression. The Astro dev toolbar seen in the review screenshots is a dev
  server artifact and is absent from the production build.
- L5: Two defects introduced by this work were fixed in their own commit:
  `::selection` referenced the undefined `var(--color-mcm-deep)` (the token is
  `--color-console-deep`), and the success checkpoint hardcoded `admin` while
  the troubleshooting branch documents a configurable bootstrap username
  (`admin` is the default when `MCM_BOOTSTRAP_ADMIN_USERNAME` is unset).
