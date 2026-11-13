---
target: web de github pages (landing)
total_score: 29
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 3
target_identity: "file:/home/fgjcarlos/projects/mcm/site/src/pages/index.astro"
target_fingerprint: "sha256:444ecc8e5f8ce09f056b3a3e88ba4867f37f717f8bd149a0b89eb1d8a18b8bce"
target_path: /home/fgjcarlos/projects/mcm/site/src/pages/index.astro
timestamp: 2026-11-12T20-55-11Z
slug: site-src-pages-index-astro
---
Method: A dual-agent (gentle-ai-explore); B single-context (verify subagent refused launch; detector + headless screenshots run in parent after A finished).
Score 29/40 (Good). H1 3, H2 3, H3 3, H4 3, H5 4, H6 2, H7 2, H8 2, H9 4, H10 3.
Specificity: copy is MCM-specific; visuals are category-interchangeable (Tailwind slate/cyan-400/amber-300 defaults, radial glow, 5x tracked eyebrow, pill buttons, rounded-3xl 3-col cards, Manrope+JetBrains Mono).
Detector: src clean (exit 0). dist: flat-type-hierarchy (false positive: /mcm base path stylesheet unresolved), nested-cards (quick-start panel with step blocks, valid).
Screens: desktop hero is text-only with empty right half; mobile eyebrow wraps "MOSQUITTO CONTROL / MANAGER"; mobile page 6634px (~8 screens).
P1 No brand mark/favicon/og:image (Layout.astro:29-32 summary_large_image without image).
P1 Palette & type are Tailwind defaults; no verified/rolled-back/failed status tokens though the app uses them.
P1 No component library: pill buttons, eyebrow x5, evidence figure x3 inline duplicated; build Astro components + /system route as storybook.
P2 Hero has no visual and no motion; DESIGN.md forbids motion beyond hover -> amend contract first; 3D must depict true story (revision -> MFA -> apply -> probe -> verified/rolled back), with poster, lazy load, reduced-motion still, no-WebGL fallback.
P2 Copy overload: alpha stated 4x, SQLite 2x, 120-word deploy caption; replace with status chips.
Personas: Jordan - "Image CI workflow" first link, jargon in Remaining work; Riley - no copy buttons, no version, localhost link before running; Casey - 900px captures illegible at 340px, tap opens raw webp, quick start ~6 screens down.
