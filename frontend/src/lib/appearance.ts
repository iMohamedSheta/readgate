// Appearance engine (goals-style) for ReadGate.
// Instant via localStorage, durable via SQLite settings table (appearance.*).

export interface Appearance {
  theme: string; // dark | light
  accent: string; // emerald | teal | sky | indigo | violet | rose | orange | amber
  font: string; // system | inter | grotesk | plex | cairo | tajawal
  codeFont: string; // jetbrains | fira | cascadia | plexmono | system
  fontSize: string; // sm | md | lg
  radius: string; // none | sharp | balanced | soft | round
  cardStyle: string; // elevated | bordered | flat | glass
  shadows: string; // on | off
  density: string; // compact | comfortable | spacious
  animations: string; // on | off
  glass: string; // on | off — frosted sidebar + sheets
}

export const DEFAULT_APPEARANCE: Appearance = {
  theme: 'dark',
  accent: 'emerald',
  font: 'system',
  codeFont: 'jetbrains',
  fontSize: 'md',
  radius: 'balanced',
  cardStyle: 'elevated',
  shadows: 'on',
  density: 'comfortable',
  animations: 'on',
  glass: 'off',
};

export const ACCENTS: Record<string, string> = {
  emerald: '16 185 129',
  teal: '45 212 191',
  sky: '56 189 248',
  indigo: '129 140 248',
  violet: '167 139 250',
  rose: '251 113 133',
  orange: '251 146 60',
  amber: '251 191 36',
};

export const THEMES = ['dark', 'light'] as const;
export const FONTS: Record<string, string> = {
  system: "Inter, ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif",
  inter: "'Inter', ui-sans-serif, system-ui, sans-serif",
  manrope: "'Manrope', ui-sans-serif, system-ui, sans-serif",
  outfit: "'Outfit', ui-sans-serif, system-ui, sans-serif",
  grotesk: "'Space Grotesk', ui-sans-serif, system-ui, sans-serif",
  plex: "'IBM Plex Sans', ui-sans-serif, system-ui, sans-serif",
  cairo: "'Cairo', ui-sans-serif, system-ui, sans-serif",
  tajawal: "'Tajawal', ui-sans-serif, system-ui, sans-serif",
};
export const FONT_SIZES: Record<string, string> = { sm: '14px', md: '15.5px', lg: '17px' };
export const MONO_FONTS: Record<string, string> = {
  jetbrains: "'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, monospace",
  fira: "'Fira Code', ui-monospace, SFMono-Regular, Menlo, monospace",
  cascadia: "'Cascadia Code', ui-monospace, SFMono-Regular, Menlo, monospace",
  plexmono: "'IBM Plex Mono', ui-monospace, SFMono-Regular, Menlo, monospace",
  sourcecode: "'Source Code Pro', ui-monospace, SFMono-Regular, Menlo, monospace",
  robotomono: "'Roboto Mono', ui-monospace, SFMono-Regular, Menlo, monospace",
  spacemono: "'Space Mono', ui-monospace, SFMono-Regular, Menlo, monospace",
  inconsolata: "'Inconsolata', ui-monospace, SFMono-Regular, Menlo, monospace",
  system: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
};
export const RADII: Record<string, string> = { none: '0px', sharp: '4px', balanced: '10px', soft: '16px', round: '22px' };

const VALID: Record<string, string[]> = {
  theme: ['dark', 'light'],
  accent: Object.keys(ACCENTS),
  font: Object.keys(FONTS),
  codeFont: Object.keys(MONO_FONTS),
  fontSize: Object.keys(FONT_SIZES),
  radius: Object.keys(RADII),
  cardStyle: ['elevated', 'bordered', 'flat', 'glass'],
  shadows: ['on', 'off'],
  density: ['compact', 'comfortable', 'spacious'],
  animations: ['on', 'off'],
  glass: ['on', 'off'],
};

const LS_KEY = 'readgate-appearance';

export function loadLocalAppearance(): Appearance {
  try {
    const raw = localStorage.getItem(LS_KEY);
    if (raw) return normalizeAppearance(JSON.parse(raw));
  } catch {}
  return { ...DEFAULT_APPEARANCE };
}

export function saveLocalAppearance(a: Appearance) {
  try {
    localStorage.setItem(LS_KEY, JSON.stringify(a));
  } catch {}
}

export function normalizeAppearance(a: any): Appearance {
  const out = { ...DEFAULT_APPEARANCE };
  for (const k of Object.keys(DEFAULT_APPEARANCE) as (keyof Appearance)[]) {
    const v = a?.[k];
    if (typeof v === 'string' && VALID[k].includes(v)) out[k] = v as never;
  }
  return out;
}

export function applyAppearance(a: Appearance) {
  const s = normalizeAppearance(a);
  const root = document.documentElement;
  root.dataset.theme = s.theme;
  root.style.colorScheme = s.theme === 'light' ? 'light' : 'dark';
  root.style.setProperty('--acc', ACCENTS[s.accent]);
  root.style.setProperty('--monofont', MONO_FONTS[s.codeFont] || MONO_FONTS.jetbrains);
  root.style.setProperty('--radius', RADII[s.radius]);
  root.style.fontSize = FONT_SIZES[s.fontSize];
  document.body.style.fontFamily = FONTS[s.font];
  root.dataset.radius = s.radius;
  root.dataset.cards = s.cardStyle;
  root.dataset.shadows = s.shadows;
  root.dataset.density = s.density;
  root.dataset.anim = s.animations;
  root.dataset.glass = s.glass;
}

// Merge backend flat map (appearance.*) over local — backend wins per key.
export function mergeSettingsMap(local: Appearance, map: Record<string, string>): Appearance {
  const out = { ...local };
  for (const k of Object.keys(DEFAULT_APPEARANCE) as (keyof Appearance)[]) {
    const v = map?.['appearance.' + k];
    if (typeof v === 'string' && VALID[k].includes(v)) out[k] = v as never;
  }
  return out;
}
