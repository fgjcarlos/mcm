// One route, drawn twice: by the SVG poster (build time) and the WebGL scene.
// x values are fractions of the content column; everything else is CSS px
// from the track centre line.

export const STATIONS = [
  { id: "review", label: "Review", x: 0.04 },
  { id: "mfa", label: "MFA", x: 0.24 },
  { id: "apply", label: "Apply", x: 0.44 },
  { id: "probe", label: "Probe", x: 0.64 },
] as const;

/** Points: the main line (new revision) diverges here onto the branch (previous revision). */
export const JUNCTION_X = 0.74;
/** Signal head on the main line; the rolled-back lamp sits below it on the branch. */
export const SIGNAL_X = 0.92;

export const BRANCH_DX = 40;
export const BRANCH_DY = 44;
export const BLADE = 22;
export const TRACK_W = 3;

export const LAMP_R = 7;
export const HOUSING_R = 10;
export const SIGNAL_R = 10;
export const SIGNAL_HOUSING_R = 13;

/** Padlock under the MFA lamp: body top edge and shackle lift when released. */
export const LOCK_Y = 20;
export const LOCK_LIFT = 5;

export const COLORS = {
  frame: "#111214",
  enamel: "#d8d2c4",
  lamp: "#e0a526",
  proceed: "#2f9e5b",
  danger: "#c8341f",
  lampOff: "#212327",
} as const;
