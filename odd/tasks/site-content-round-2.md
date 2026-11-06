# Site content round 2 — plain language, no duplicated reading, a usable ending

Second Impeccable round on the public landing, after the reorganization
merged in PR #345. Source of truth: the fresh dual-agent review of the merged
site (`.impeccable/critique/`), which scored the landing 25/32 with the two
original P1 findings fixed and two P2 findings only partially fixed.

Decision of the operator for this round: run the technical audit dimension
(which had never been assessed), then fix the three content P2 findings. The
responsive evidence redesign (`adapt`) stays for a later round, but the
deployment-history detail is written in HTML so the mobile reading carries the
claim.

## Specs

- S1 (`/impeccable clarify`, P2 "plain-language orientation stops too early"):
  the page must not ask a first-time visitor to decode operator vocabulary.
  Concretely: replace the lede that restates the headline with a short
  statement of what a Mosquitto broker is and what MCM adds; replace
  "graphical configuration MVP" with wording that says the graphical
  configuration interface is still incomplete; explain the recent MFA
  requirement as a fresh authenticator check; introduce the bootstrap
  credentials as the initial administrator sign-in; name the purpose of
  "listeners", "broker TLS material", "bridges" and "Dynamic Security" the
  first time each appears; expand "SSO" and "diagnostics inspect but do not
  enforce"; and give Git, Docker Compose and Task a named setup link each.
  Technical terms may stay, but never unaccompanied at first use.
- S2 (`/impeccable distill`, P2 "the inventory is deduplicated, but the page
  still explains the same evidence twice"): the availability map keeps its
  three categories, but "Available controls" becomes short scannable rows
  instead of a semicolon-separated paragraph of seven capability groups. The
  separate "Workflow" section goes away: the two operator checks are folded
  into the captions of the captures they describe, because the sentence
  "The captures above show the product in use; these are the two checks they
  illustrate" states the duplication outright, and the MQTT user directory
  capture is not represented by either card. If two cards survive anywhere,
  they use two desktop columns, not three.
- S3 (`/impeccable onboard`, P2 "the final action is less usable than the
  opening action"): the quick start is ordered open → sign in → confirm
  connected, instead of leading with the result; `http://localhost:8080`
  becomes a real link rather than inert code; a "signed in, but the broker
  shows disconnected" branch is added pointing at the existing diagnostics
  path; `task down` stays as the explicit exit; and the page ends on that
  bounded success instead of trailing off into infrastructure and resource
  links.
- S4 (operator decision: "Detalle del historial en HTML"): the deployment
  history gets a legible HTML detail — what was verified, what rolled back
  and why — so a reader on a 390px screen gets the claim from text rather
  than from an unreadable screenshot. The authentic capture stays as the
  artifact that proves it; nothing is manufactured and no success-only
  screen is invented. The raw `active_verified` token stops being the only
  translation available.
- S5 (P3s from the same review): the hero stops spending a line on a single
  trailing word and stops restating the headline in the lede; the link to
  `docs/production.md` gets a persistent underline and a human label
  ("Production deployment guide"); and `DESIGN.md` is reconciled with the
  implementation, because it still describes the old OKLCH declarations, the
  literal gradient in the layout and the removed "What MCM is"/stack split.
- S6 (audit): the technical quality dimensions that were never assessed —
  rendered contrast, touch target sizes, focus visibility, responsive
  behaviour at 390/768/1440, semantic structure, and first-viewport loading —
  are measured on the rendered page and every finding the audit raises is
  fixed, or explicitly recorded as accepted with a reason.

## Tasks

| ID | Specs | Route | Commit |
|----|-------|-------|--------|
| T1 | S6 | parent (read-only audit) + gpt-luna-6 (fixes) | — |
| T2 | S1, S5 | gpt-luna-6 (site/src/**, DESIGN.md) | — |
| T3 | S2, S4 | gpt-luna-6 (site/src/**) | — |
| T4 | S3 | gpt-luna-6 (site/src/**) | — |
| T5 | verify: build, detector, audit re-run, re-critique | parent | — |
| T6 | issue + branch + PR | parent (user decides) | — |

## Log

- L1: "que podemos hacer para mejorar el diseño de la web? https://github.com/pbakaus/impeccable con esto como fue?" (verbatim).
- L2: Fresh review of the merged landing: 25/32, unchanged from the previous
  round. Both original P1 findings fixed; P2 evidence and P2 quick start
  fixed; P2 plain language and P2 duplicated reading only partially fixed.
  The evidence is now credible (the deployment history shows verified,
  rolled-back and failed states) but the reading sequence is not yet equally
  precise.
- L3: Operator decisions for this round: run the audit dimension and fix the
  three content P2 findings; the responsive evidence redesign stays for a
  later round, but the deployment history detail is written in HTML so the
  mobile reading carries the claim.
- L4: The audit dimension had never been assessed for this project: `audit`
  is a skill playbook and not a CLI verb, and the detector's URL scan cannot
  attach to the Chromium on this host. The detector's source scan is clean
  (exit 0, no findings) and the built output measures 306 KB, with the
  fonts, images, metadata and every link resolving, but none of that covers
  rendered contrast or focus behaviour.
- L5: The audit ran the playbook against the rendered page and reported three
  P1 findings. Two were not defects and one was real but worse than
  reported, so each was re-measured on the current source before anything
  was changed:
  - "The requested route returns HTTP 500" was a transient state while the
    implementation was being edited: the dev server log shows
    `FeatureSection is not defined` at the moment the component import was
    removed before its last use, and the server recovered on its own. The
    auditor's preview also served stale output. Not a defect.
  - "The primary CTA focus outline measures 1.79:1" was measured without
    focus applied and against that stale preview. Measured with
    `:focus-visible` active on the current source, the outline resolves to
    cyan-200, 2px solid, 4px offset. Not a defect.
  - "The header link measures 4.04:1" was real and worse: sampling the
    rendered pixel behind the text gives 3.56:1 at 1440px and 3.34:1 at
    390px, below the 4.5:1 that normal text needs. Fixed by moving the link
    to the ink-soft token, which measures 6.30:1 and 5.91:1.
- L6: The palette drift was real: the `@theme` tokens declared a colour
  system that no component used while every component hardcoded Tailwind
  utilities. The page now uses token utilities only, the orphaned
  `FeatureSection` component is gone, and the token values are expressed in
  Tailwind's own colour space so the utilities reproduce exactly what they
  reproduced before. A pixel-diff of the full page before and after the
  migration shows zero change in the accent, the field, the panels and the
  fills; the residual is the CTA label and the gradient interpolation at
  one unit per channel, which the token values in Tailwind's colour space
  explain. The visual record was corrected with the values the browser
  actually renders, which are not the Tailwind v3 hex values the record
  previously claimed.
